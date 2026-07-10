# Password Hook Service

Password Hook Service is the Phase 1 migration service described in `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`.

It accepts successful LDAP login credentials from the portal, authenticates requests with HMAC-SHA256, skips external-email identities, and enqueues eligible internal student/employee IDs for password sync to Microsoft Entra ID.

## Current Scope

This service currently implements the HTTP hook, encrypted Service Bus queueing, worker retry/DLQ handling, and Microsoft Graph password sync path:

- Go module and package structure
- `GET /healthz`
- `GET /version`
- `POST /api/v1/hook/password`
- HMAC request-signing middleware
- RFC 9457 problem responses
- password/secret masking helper
- request ID propagation
- source allowlist and anomalous request rate limiting
- identity classification and UPN building primitives
- Azure Service Bus producer for eligible internal student/employee IDs
- 300 second Service Bus message TTL for password sync jobs
- explicit runtime secret loading from local env or Azure Key Vault
- encrypted queue payloads for password sync messages
- Service Bus worker consumption with retry and safe DLQ handling
- Microsoft Graph app-only client for existing-user password patches and missing-user creation
- backend-neutral observability recorder boundary
- structured hook, worker, and Graph outcome events
- trace ID propagation through queue messages
- queue and safe-DLQ depth probe boundaries
- Azure Monitor exporter mode for OTLP traces and custom metrics

Terraform resources and CI/CD security gates are implemented in later slices.

## Local Verification

Run the standard local verification:

```bash
make verify
```

The Makefile wraps the Dockerized Go toolchain and runs the container as your
host UID/GID to avoid root-owned generated files. The equivalent raw command is:

```bash
docker run --rm --user "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -e GOCACHE=/tmp/go-build \
  -e GOMODCACHE=/tmp/go/pkg/mod \
  -v "$(pwd):/src" \
  -w /src \
  golang:1.26.4 \
  sh -c "gofmt -w . && go test ./... && go vet ./..."
```

## Local Run

Set the required environment variables:

```bash
export SECRETS_SOURCE="env"
export HOOK_HMAC_SECRET="local-development-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export PROBLEM_BASE_URL="https://nycu.edu.tw/problems"
export HTTP_ADDR=":8080"
export SERVICEBUS_CONNECTION_STRING="<redacted-send-only-service-bus-connection-string>"
export SERVICEBUS_QUEUE_NAME="password-sync"
export SERVICEBUS_DEADLETTER_QUEUE_NAME="password-sync-dlq"
export PASSWORD_ENCRYPTION_KEY_B64="<base64-encoded-32-byte-key>"
export PASSWORD_ENCRYPTION_KEY_ID="password-payload-key-v1"
export GRAPH_TENANT_ID="<tenant-id>"
export GRAPH_CLIENT_ID="<app-client-id>"
export GRAPH_CLIENT_SECRET="<app-client-secret>"
export PORTAL_ALLOWED_CIDRS="127.0.0.1/32,::1/128"
export RATE_LIMIT_PER_IP="500"
export RATE_LIMIT_WINDOW="1s"
export HOOK_MAX_BODY_BYTES="65536"
```

Production `app.New` requires `GRAPH_TENANT_ID`, `GRAPH_CLIENT_ID`, and `GRAPH_CLIENT_SECRET`. The Graph app registration needs the approved application permission `User.ReadWrite.All`.

Use a queue- or topic-level Shared Access Policy with the permissions needed by this runtime to send hook messages, receive worker messages, and send safe DLQ messages. Do not use namespace-level manage policies for application runtime credentials.

Run the service:

```bash
docker build -f deploy/Dockerfile -t password-hook-service .
docker run --rm -p 8080:8080 \
  -e SECRETS_SOURCE \
  -e HOOK_HMAC_SECRET \
  -e ENTRA_PRIMARY_DOMAIN \
  -e PROBLEM_BASE_URL \
  -e HTTP_ADDR \
  -e SERVICEBUS_CONNECTION_STRING \
  -e SERVICEBUS_QUEUE_NAME \
  -e SERVICEBUS_DEADLETTER_QUEUE_NAME \
  -e PASSWORD_ENCRYPTION_KEY_B64 \
  -e PASSWORD_ENCRYPTION_KEY_ID \
  -e GRAPH_TENANT_ID \
  -e GRAPH_CLIENT_ID \
  -e GRAPH_CLIENT_SECRET \
  -e PORTAL_ALLOWED_CIDRS \
  -e RATE_LIMIT_PER_IP \
  -e RATE_LIMIT_WINDOW \
  -e HOOK_MAX_BODY_BYTES \
  password-hook-service
```

Check health:

```bash
curl -i http://localhost:8080/healthz
```

Expected response:

```json
{"status":"ok"}
```

## Azure Key Vault Secret Loading

Production uses Managed Identity through Azure SDK `DefaultAzureCredential`.

```bash
export SECRETS_SOURCE="keyvault"
export KEY_VAULT_URL="https://<vault-name>.vault.azure.net/"
export KEY_VAULT_HMAC_SECRET_NAME="hook-hmac-secret"
export KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME="servicebus-conn-str"
export KEY_VAULT_GRAPH_CLIENT_SECRET_NAME="graph-client-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export ENTRA_FALLBACK_DOMAIN="nycu.onmicrosoft.com"
export GRAPH_TENANT_ID="<tenant-id>"
export GRAPH_CLIENT_ID="<app-client-id>"
export SERVICEBUS_QUEUE_NAME="password-sync"
```

The managed identity assigned to the container app must have `secrets/get` permission for the configured Key Vault. Local development must opt into `SECRETS_SOURCE=env`; the service does not silently fall back from Key Vault to environment secrets.

## Local HMAC Request

Generate request headers and a sample JSON body:

```bash
HOOK_HMAC_SECRET="local-development-secret" php docs/examples/sign-hook-request.php
```

Use the printed `X-Hook-Timestamp`, `X-Hook-Nonce`, and `X-Hook-Signature` headers with:

```bash
curl -i http://localhost:8080/api/v1/hook/password \
  -H "Content-Type: application/json" \
  -H "X-Hook-Timestamp: <printed timestamp>" \
  -H "X-Hook-Nonce: <printed nonce>" \
  -H "X-Hook-Signature: <printed signature>" \
  --data '{"cn":"311551001","password":"cleartext_password","displayName":"Test User","mail":"test@nycu.edu.tw","eventType":"login_bootstrap"}'
```

The hook endpoint returns `202 Accepted` when the request is accepted by the service. It does not mean the password has already been migrated to Entra ID.

## API Protection

`POST /api/v1/hook/password` is protected in application middleware before the hook handler runs:

- Requests from sources outside `PORTAL_ALLOWED_CIDRS` return `401 Unauthorized`.
- Requests above `RATE_LIMIT_PER_IP` during `RATE_LIMIT_WINDOW` return `429 Too Many Requests`.
- HMAC authentication failures return `401 Unauthorized` with a generic problem detail.
- Request bodies larger than `HOOK_MAX_BODY_BYTES` return `413 Payload Too Large`.

The portal and password hook service share the HMAC secret for this API. The portal signs each hook request with `X-Hook-Timestamp`, `X-Hook-Nonce`, and `X-Hook-Signature`; the hook service verifies the same secret before accepting the body as authentic. Keep this secret in the approved secret store for production and out of source code.

Each hook request represents one successful-login password event for one LDAP identity. The service can process a single end-user event, but Slice 9 does not add per-user rate limiting. Any future per-user limiter must run after HMAC succeeds because only then can the service trust signed body fields such as `cn`.

For the current portal topology, configure `PORTAL_ALLOWED_CIDRS` to the full `/32` CIDRs for the two portal web-server egress addresses (`<portal-egress-ip-1>` and `<portal-egress-ip-2>` as currently described). `RATE_LIMIT_PER_IP` is enforced per immediate portal web-server source IP. With two portal web servers, the expected aggregate cap is approximately `2 * RATE_LIMIT_PER_IP` when traffic is evenly balanced.

The application intentionally does not use `X-Forwarded-For` as the anomaly rate-limit key in this slice. The goal is to catch abnormal hook output from either portal web server, including retry loops, bugs, or uneven load balancer distribution.

Size `RATE_LIMIT_PER_IP` from observed peak successful-login hook rate per portal web server, with enough headroom that normal login bursts do not receive `429`. The default `500` is a guardrail, not a fixed production capacity decision. The portal must not fail user login on `429`, and it must not immediately retry in a tight loop.

Infrastructure protections such as Azure Front Door, WAF rules, Azure DDoS Protection, private endpoints, VPN routing, and Terraform ingress policy are outside this application slice and are handled by later infrastructure slices.

## Worker Behavior

The production app starts the HTTP server and password sync worker together. The hook reads password JSON into mutable buffers, encrypts accepted password payloads before enqueueing, and zeroes plaintext buffers before returning. The worker receives encrypted Service Bus messages, decrypts the password per processing attempt, calls Microsoft Graph with borrowed plaintext bytes, and zeroes plaintext/message buffers before retry, DLQ, or settlement.

Graph `400` and `403` responses are treated as permanent processor failures and recorded to the safe DLQ. Graph `429`, `503`, other unexpected statuses, token acquisition errors, and network errors remain retryable under the worker retry policy. Safe DLQ entries exclude plaintext passwords.

Structured logging masks password, password-derived, secret, and token fields by key before records are emitted.

## Event Types and Sync Status

Every hook request must include an `eventType` field with one of three values:

- `login_bootstrap` - sent after a user completes SSO login and the portal bootstraps their on-prem AD account. The service skips re-enqueueing this event if the UPN is already marked `synced`, or has a `sync_pending` record fresher than the internal pending-sync TTL (300s by default). This avoids redundant AD writes on every login.
- `password_change` - sent when a user changes their password. Always enqueued, regardless of prior sync status.
- `password_recovery` - sent when a user recovers or resets their password. Always enqueued, regardless of prior sync status.

Sync status (`unsynced` / `sync_pending` / `synced` / `sync_failed`) is tracked per-UPN by `internal/syncstatus.MemoryStore`, an in-process, non-durable store: it resets on process restart and is not shared across replicas. This is a deliberate Slice 7A scope limit; Slice 10 (infrastructure) introduces durable, shared sync-status storage. See `docs/superpowers/specs/2026-06-24-password-hook-service-design.md` section 1.2.1 Amendment for the full event model rationale.

## Observability

The service emits structured JSON logs through `log/slog` and records metrics through the backend-neutral `internal/observability.Recorder` interface. By default, production wires a no-op recorder. Set `OBSERVABILITY_EXPORTER=azure_monitor` to export traces through OpenTelemetry OTLP and publish custom metrics to Azure Monitor.

Key structured actions:

| Action | Meaning |
|--------|---------|
| `hook_password_sync_accepted` | A hook request was accepted and enqueued. |
| `hook_password_sync_skipped` | A hook request was accepted but skipped, for example external email identity or Slice 7A sync-status dedupe. |
| `hook_password_sync_rejected` | A hook request failed validation or acceptance. |
| `middleware_request_rejected` | HMAC, source allowlist, or rate-limit middleware rejected a request before it reached the hook. |
| `middleware_panic_recovered` | Recovery middleware handled a panic and returned an RFC 9457 500 response. |
| `worker_password_sync_completed` | The worker completed a password sync and marked the account synced. |
| `worker_password_sync_failed` | The worker recorded a terminal safe-DLQ outcome and marked sync failed. |
| `worker_message_invalid` | The worker completed an invalid queue message after writing a password-safe DLQ record. |
| `worker_message_abandoned` | The worker abandoned a message because retry backoff was canceled or safe-DLQ recording failed. |
| `graph_password_upsert` | The Graph processor attempted a create/update password operation. |

Metric names:

| Metric | Type | Labels |
|--------|------|--------|
| `hook_requests_total` | counter | `status`, `outcome`, optional `eventType`, optional `identityType`, optional `reason` |
| `migration_skipped_total` | counter | `status`, `outcome`, `eventType`, `identityType`, `reason` |
| `middleware_requests_total` | counter | `middleware`, `status`, `outcome`, optional `reason` |
| `worker_messages_total` | counter | `outcome`, optional `eventType`, optional `reason`, optional `attempts` |
| `graph_upsert_duration_seconds` | duration | `outcome` |
| `queue_depth` | gauge | `queue`, `kind` |

Logs and metric labels must not include cleartext passwords, encrypted password fields, request bodies, Service Bus message bodies, Graph request bodies, HMAC secrets, HMAC signatures, nonces, or authorization headers.

## Azure Monitor Export

Set `OBSERVABILITY_EXPORTER=azure_monitor` to export production telemetry.

Logs and traces:

- Configure the Azure Container Apps managed OpenTelemetry agent to send logs and traces to Application Insights.
- Set `OTEL_EXPORTER_OTLP_ENDPOINT` to the agent endpoint.

Metrics:

- Set `AZURE_MONITOR_METRIC_RESOURCE_ID` to the Azure resource ID that owns custom metrics.
- Set `AZURE_MONITOR_METRIC_REGION` to the resource region.
- Set `AZURE_MONITOR_METRIC_NAMESPACE` to the namespace used for custom metrics, defaulting to `password-hook-service`.
- Assign the runtime managed identity the `Monitoring Metrics Publisher` role for that resource.

Azure Container Apps Application Insights OpenTelemetry destination does not currently export metrics; this service publishes custom metrics through Azure Monitor's custom metrics REST API.

Example verification queries depend on the deployed workspace, but the expected metric namespace is `password-hook-service` and expected metric names include `hook_requests_total`, `middleware_requests_total`, `worker_messages_total`, `graph_upsert_duration_seconds`, and `queue_depth`.

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `SECRETS_SOURCE` | empty | Required; `env` for explicit local fallback or `keyvault` for Azure Key Vault |
| `KEY_VAULT_URL` | empty | Required when `SECRETS_SOURCE=keyvault` |
| `KEY_VAULT_HMAC_SECRET_NAME` | `hook-hmac-secret` | Key Vault secret name for the HMAC shared secret |
| `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME` | `servicebus-conn-str` | Key Vault secret name for the Service Bus connection string |
| `KEY_VAULT_GRAPH_CLIENT_SECRET_NAME` | `graph-client-secret` | Key Vault secret name for the Graph client secret |
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOOK_HMAC_SECRET` | empty | HMAC shared secret when `SECRETS_SOURCE=env` |
| `ENTRA_PRIMARY_DOMAIN` | `nycu.edu.tw` | Domain used to build internal Entra UPNs |
| `ENTRA_FALLBACK_DOMAIN` | empty | Optional fallback domain for later tenant bootstrap scenarios |
| `GRAPH_TENANT_ID` | empty | Required for production; Microsoft Entra tenant ID for Graph app-only auth |
| `GRAPH_CLIENT_ID` | empty | Required for production; app registration client ID for Graph app-only auth |
| `GRAPH_CLIENT_SECRET` | empty | Graph app client secret when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
| `SERVICEBUS_CONNECTION_STRING` | empty | Azure Service Bus connection string when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
| `SERVICEBUS_DEADLETTER_QUEUE_NAME` | `password-sync-dlq` | Safe DLQ queue name for terminal password sync failures |
| `PASSWORD_ENCRYPTION_KEY_B64` | empty | Required; base64-encoded 32-byte AES-GCM key for queued password payloads |
| `PASSWORD_ENCRYPTION_KEY_ID` | `password-payload-key-v1` | Required; key identifier embedded in encrypted queue messages |
| `PORTAL_ALLOWED_CIDRS` | empty | Required; comma-separated source CIDR allowlist for portal web-server egress IPs |
| `RATE_LIMIT_PER_IP` | `500` | Optional; defaults to `500`; must be positive when set; per-source-IP request threshold during `RATE_LIMIT_WINDOW`; with two portal web servers, aggregate capacity is approximately `2 * RATE_LIMIT_PER_IP` |
| `RATE_LIMIT_WINDOW` | `1s` | Optional; defaults to `1s`; must be a positive Go duration when set; anomaly rate-limit window |
| `HOOK_MAX_BODY_BYTES` | `65536` | Optional; defaults to `65536`; must be a positive byte limit when set; signed hook request body limit |
| `OBSERVABILITY_EXPORTER` | `none` | Optional; set to `azure_monitor` to enable Azure Monitor telemetry export |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; Azure Container Apps managed OpenTelemetry agent endpoint for traces |
| `AZURE_MONITOR_METRIC_RESOURCE_ID` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; Azure resource ID that owns custom metrics |
| `AZURE_MONITOR_METRIC_REGION` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; Azure region for the custom metrics endpoint |
| `AZURE_MONITOR_METRIC_NAMESPACE` | `password-hook-service` | Custom metrics namespace when Azure Monitor export is enabled |
