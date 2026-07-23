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

## Infrastructure

The production deployment uses a private Application Gateway WAF_v2 as the sole ingress point. Portal web servers reach the service over the existing S2S VPN; no public ACA ingress is exposed.

**Ingress topology:**
- An Azure Application Gateway WAF_v2 listener is bound to a private frontend IP inside the gateway subnet. Portal web servers reach it over TCP 443 through the existing S2S VPN. Public authoritative DNS continues to serve the existing public LDAP frontend; on-premises split-horizon DNS resolves the private password-hook hostname to the Application Gateway private frontend IP only for approved portal callers.
- The gateway-to-ACA hop is HTTPS only. The backend uses the ACA ingress FQDN as the host header, SNI, and probe host; TLS is never downgraded.
- A listener-specific WAF policy runs in Prevention mode with OWASP 3.2 and BotManager 0.1 managed rule sets, plus a priority-10 custom Block rule that rejects any source not in `PORTAL_ALLOWED_CIDRS` before the managed rules run. The full WAF contract is in [`deploy/terraform/application-gateway-handoff.md`](deploy/terraform/application-gateway-handoff.md).
- The Application Gateway is managed by the external owner pipeline in [`lyy-nycu/ldap-service`](https://github.com/lyy-nycu/ldap-service); this repository only emits the handoff contract values.

**Prerequisites before the first deploy:**
- A TLS certificate covering the private hostname must be pre-provisioned in the shared Application Gateway certificate store (the existing ACME renewal workflow in `lyy-nycu/ldap-service` owns this).
- On-premises split-horizon DNS must be configured to return the private frontend IP for the password-hook hostname.
- The S2S VPN and routes permitting TCP 443 to the private frontend IP must be in place.

**Container App and ACR:**
- The Container App runs in the existing shared ACA managed environment (`cae-stg-jpe-001` / `cae-prod-jpe-001`). This repository does not create the environment.
- The Container App uses `external_enabled = true` ingress. Azure's "internal" ingress mode only allows calls from other Container Apps within the same environment and is not reachable by an external reverse proxy like the Application Gateway (confirmed by staging validation), so external ingress is required for the AGW backend pool to work. The app is still never reachable from the public internet: the shared ACA environment itself has no public inbound IP (internal-only VNet configuration), which is the actual security boundary.
- Container images are pulled from the existing shared ACR `acrjpe001` (resource group `rg-acr-jpe-001`) via identity-based `AcrPull`. This repository does not create the ACR.

**Service Bus (managed identity, no connection string in production):**
- `SERVICEBUS_AUTH_MODE=managed_identity`. The runtime UAMI holds `Azure Service Bus Data Sender` on the active and safe-DLQ queues and `Azure Service Bus Data Receiver` on the active queue — all queue-scoped. No connection string or SAS is used in production.

**Azure Managed Redis (Entra auth, no access key in production):**
- `SYNC_STATUS_STORE=redis`. The runtime UAMI authenticates via Entra ID (`azurerm_managed_redis_access_policy_assignment`); access-key authentication is disabled on the instance. See the [Event Types and Sync Status](#event-types-and-sync-status) section for the data model.

**Observability:**
- `OBSERVABILITY_EXPORTER=azure_monitor`. The managed OpenTelemetry agent is configured on the shared ACA environment (merge-PATCH via `azapi_update_resource`); it injects `OTEL_EXPORTER_OTLP_ENDPOINT` automatically. Custom metrics go to Azure Monitor via the custom-metrics REST API, requiring `Monitoring Metrics Publisher` at the Container App ARM resource scope. See the [Azure Monitor Export](#azure-monitor-export) section for details.

**Terraform:**
- Terraform configuration is in `deploy/terraform/`. The deployment sequence, identity and RBAC model, Redis retention model, and the on-premises validation walkthrough are in [`deploy/terraform/README.md`](deploy/terraform/README.md). No secret values, connection strings, or access keys are stored in Terraform state, variable files, or outputs.

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

## CI/CD

Every pull request and push to `main` runs six required GitHub Actions
checks: `test` (unit tests, vet, and a report-only coverage summary),
`gosec`, `govulncheck`, `trivy-fs`, `gitleaks` (all four upload SARIF
results to the repository's Security → Code scanning alerts tab), and a
conditional `terraform-check`. `main` has branch protection requiring all
six to pass before merge.

On push to `main`, `.github/workflows/cd.yml` builds, scans, and pushes a
container image to the existing ACR, then runs Terraform against real
staging via Azure OIDC. See
[`deploy/terraform/README.md`](deploy/terraform/README.md#continuous-deployment-cd-to-staging)
for the full pipeline description, the `TF_APPLY_MODE` plan/apply safety
valve, and the dedicated CD identity's RBAC. Production deployment remains a
manual `terraform apply`, out of scope for this automated pipeline.

## Local Run

For local development with connection string (fallback):

```bash
export SECRETS_SOURCE="env"
export HOOK_HMAC_SECRET="local-development-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export PROBLEM_BASE_URL="https://nycu.edu.tw/problems"
export HTTP_ADDR=":8080"
export SERVICEBUS_AUTH_MODE="connection_string"
export SERVICEBUS_CONNECTION_STRING="<redacted-send-only-service-bus-connection-string>"
export SERVICEBUS_QUEUE_NAME="password-sync"
export SERVICEBUS_DEADLETTER_QUEUE_NAME="password-sync-dlq"
export PASSWORD_MESSAGE_TTL="5m"
export SYNC_STATUS_STORE="memory"
export PASSWORD_ENCRYPTION_KEY_B64="<base64-encoded-32-byte-key>"
export PASSWORD_ENCRYPTION_KEY_ID="password-payload-key-v1"
export GRAPH_TENANT_ID="<tenant-id>"
export GRAPH_CLIENT_ID="<app-client-id>"
export GRAPH_CLIENT_SECRET="<app-client-secret>"
export PORTAL_ALLOWED_CIDRS="127.0.0.1/32,::1/128"
export DIRECT_CLIENT_MODE="true"
export RATE_LIMIT_PER_IP="500"
export RATE_LIMIT_WINDOW="1s"
export HOOK_MAX_BODY_BYTES="65536"
```

For production with managed identity:

```bash
export SECRETS_SOURCE="keyvault"
export SERVICEBUS_AUTH_MODE="managed_identity"
export SERVICEBUS_NAMESPACE_FQDN="<namespace>.servicebus.windows.net"
export SERVICEBUS_QUEUE_NAME="password-sync"
export SERVICEBUS_DEADLETTER_QUEUE_NAME="password-sync-dlq"
export PASSWORD_MESSAGE_TTL="5m"
export SYNC_STATUS_STORE="redis"
export REDIS_HOST="<managed-redis-host>"
export REDIS_PORT="<tls-port>"
export REDIS_KEY_PREFIX="password-hook:sync-status:"
export SYNC_STATUS_TERMINAL_TTL="2160h"
export AZURE_CLIENT_ID="<runtime-uami-client-id>"
export DIRECT_CLIENT_MODE="false"
export TRUSTED_PROXY_CIDRS="<observed-immediate-proxy-cidr>"
```

Production `app.New` requires `GRAPH_TENANT_ID`, `GRAPH_CLIENT_ID`, and `GRAPH_CLIENT_SECRET`. The Graph app registration needs the approved application permission `User.ReadWrite.All`.

### Service Bus Managed Identity

Production uses the Container App managed identity rather than a Service Bus connection string or Shared Access Signature (SAS). Assign the identity the narrowest Service Bus RBAC roles needed to send active-queue hook messages, receive and settle active-queue worker messages, and send safe-DLQ messages. Scope roles to the relevant queue or topic where supported; do not grant namespace-level management access to the application runtime.

`SERVICEBUS_AUTH_MODE=connection_string` and its SAS connection string remain supported for local development and an explicitly approved emergency rollback only. They are not the production authentication path.

Run the service:

```bash
docker build -f deploy/Dockerfile -t password-hook-service .
docker run --rm -p 8080:8080 \
  -e SECRETS_SOURCE \
  -e HOOK_HMAC_SECRET \
  -e ENTRA_PRIMARY_DOMAIN \
  -e PROBLEM_BASE_URL \
  -e HTTP_ADDR \
  -e SERVICEBUS_AUTH_MODE \
  -e SERVICEBUS_CONNECTION_STRING \
  -e SERVICEBUS_NAMESPACE_FQDN \
  -e SERVICEBUS_QUEUE_NAME \
  -e SERVICEBUS_DEADLETTER_QUEUE_NAME \
  -e PASSWORD_MESSAGE_TTL \
  -e SYNC_STATUS_STORE \
  -e REDIS_HOST \
  -e REDIS_PORT \
  -e REDIS_KEY_PREFIX \
  -e SYNC_STATUS_TERMINAL_TTL \
  -e AZURE_CLIENT_ID \
  -e PASSWORD_ENCRYPTION_KEY_B64 \
  -e PASSWORD_ENCRYPTION_KEY_ID \
  -e GRAPH_TENANT_ID \
  -e GRAPH_CLIENT_ID \
  -e GRAPH_CLIENT_SECRET \
  -e PORTAL_ALLOWED_CIDRS \
  -e TRUSTED_PROXY_CIDRS \
  -e DIRECT_CLIENT_MODE \
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
# Required only when SERVICEBUS_AUTH_MODE=connection_string.
export KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME="servicebus-conn-str"
export KEY_VAULT_GRAPH_CLIENT_SECRET_NAME="graph-client-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export ENTRA_FALLBACK_DOMAIN="nycu.onmicrosoft.com"
export GRAPH_TENANT_ID="<tenant-id>"
export GRAPH_CLIENT_ID="<app-client-id>"
export SERVICEBUS_QUEUE_NAME="password-sync"
```

The managed identity assigned to the container app must have `secrets/get` permission for the configured Key Vault. Local development must opt into `SECRETS_SOURCE=env`; the service does not silently fall back from Key Vault to environment secrets.

When using `SERVICEBUS_AUTH_MODE=managed_identity`, `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME` is not required. The managed identity authenticates to Service Bus through RBAC instead of a connection string.

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

For local or test traffic that reaches the process directly, set `DIRECT_CLIENT_MODE=true` and leave `TRUSTED_PROXY_CIDRS` empty. Production must set `DIRECT_CLIENT_MODE=false` and configure `TRUSTED_PROXY_CIDRS` only with the immediate ACA/Application Gateway proxy peers observed and approved during staging. The two modes are mutually exclusive. `TRUSTED_PROXY_CIDRS` must not contain an unrestricted network such as `0.0.0.0/0` or `::/0`, and must not overlap `PORTAL_ALLOWED_CIDRS`; the service rejects startup with a config error rather than let a direct portal peer be treated as a trusted proxy and forge `X-Forwarded-For`.

When the immediate peer is untrusted, the application ignores `X-Forwarded-For` and uses the peer address. When it is trusted, the application strictly validates the complete forwarded chain from the nearest hop toward the client. Missing, malformed, ambiguous, or all-trusted chains fail closed. The resolved address is independently checked against `PORTAL_ALLOWED_CIDRS` and used as the per-client rate-limit key. WAF also enforces the portal CIDRs at Application Gateway; neither layer replaces the other. Before production rollout, record the sanitized staging peer/header shape and stop if it cannot satisfy this trust-boundary algorithm instead of widening the trusted range.

Size `RATE_LIMIT_PER_IP` from observed peak successful-login hook rate per portal web server, with enough headroom that normal login bursts do not receive `429`. The default `500` is a guardrail, not a fixed production capacity decision. The portal must not fail user login on `429`, and it must not immediately retry in a tight loop.

## Worker Behavior

The production app starts the HTTP server and password sync worker together. The hook reads password JSON into mutable buffers, encrypts accepted password payloads before enqueueing, and zeroes plaintext buffers before returning. The worker receives encrypted Service Bus messages, decrypts the password per processing attempt, calls Microsoft Graph with borrowed plaintext bytes, and zeroes plaintext/message buffers before retry, DLQ, or settlement.

Graph `400` and `403` responses are treated as permanent processor failures and recorded to the safe DLQ. Graph `429`, `503`, other unexpected statuses, token acquisition errors, and network errors remain retryable under the worker retry policy. Safe DLQ entries exclude plaintext passwords.

Structured logging masks password, password-derived, secret, and token fields by key before records are emitted.

## Event Types and Sync Status

Every hook request must include an `eventType` field with one of three values:

- `login_bootstrap` - sent after a user completes SSO login and the portal bootstraps their on-prem AD account. The service skips re-enqueueing this event if the UPN is already marked `synced`, or has a `sync_pending` record fresher than `PASSWORD_MESSAGE_TTL` (`5m` by default). This avoids redundant AD writes on every login.
- `password_change` - sent when a user changes their password. Always enqueued, regardless of prior sync status.
- `password_recovery` - sent when a user recovers or resets their password. Always enqueued, regardless of prior sync status.

Production uses Azure Managed Redis with Entra authentication and TLS for shared sync status across replicas and revisions. Redis keys contain a SHA-256 digest of the normalized UPN, never the raw UPN; values contain only status, `UpdatedAt`, and `SourceEnqueuedAt`. Equal source timestamps use the monotonic precedence `sync_pending < sync_failed < synced`, and idempotent writes do not refresh timestamps or TTLs.

Pending records expire with `PASSWORD_MESSAGE_TTL`. Terminal `synced` and `sync_failed` records expire after the owner-approved `SYNC_STATUS_TERMINAL_TTL` of `90d` (`2160h`); after expiration a later `login_bootstrap` can enqueue again. Redis is operational deduplication state, not a password store, audit record, or permanent history. A Redis read outage fails open and can cause a duplicate Graph sync; status writes remain best-effort. Passwords, queue payloads, HMAC material, tokens, Redis access keys, and raw UPNs must never be stored in this Redis data.

Set `SYNC_STATUS_STORE=memory` only for explicit local/test use. `MemoryStore` is non-durable, resets on process restart, and is not shared across replicas.

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
- ACA injects `OTEL_EXPORTER_OTLP_ENDPOINT` into every container automatically once the managed agent is configured on the managed environment. Do NOT set this variable explicitly in Terraform or hand-authored deployment configuration — the service reads only the runtime-injected value. Setting it explicitly would compete with the injected value and can silently route traces to the wrong endpoint.
- The exporter uses OTLP over gRPC to the local in-cluster sidecar the managed agent provides.

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
| `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME` | `servicebus-conn-str` | Service Bus connection-string secret name; required only when `SERVICEBUS_AUTH_MODE=connection_string` |
| `KEY_VAULT_GRAPH_CLIENT_SECRET_NAME` | `graph-client-secret` | Key Vault secret name for the Graph client secret |
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOOK_HMAC_SECRET` | empty | HMAC shared secret when `SECRETS_SOURCE=env` |
| `ENTRA_PRIMARY_DOMAIN` | `nycu.edu.tw` | Domain used to build internal Entra UPNs |
| `ENTRA_FALLBACK_DOMAIN` | empty | Optional fallback domain for later tenant bootstrap scenarios |
| `GRAPH_TENANT_ID` | empty | Required for production; Microsoft Entra tenant ID for Graph app-only auth |
| `GRAPH_CLIENT_ID` | empty | Required for production; app registration client ID for Graph app-only auth |
| `GRAPH_CLIENT_SECRET` | empty | Graph app client secret when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
| `SERVICEBUS_AUTH_MODE` | `connection_string` | `connection_string` for local or emergency rollback, or `managed_identity` for production Service Bus RBAC authentication |
| `SERVICEBUS_CONNECTION_STRING` | empty | Local or rollback Service Bus connection string when `SERVICEBUS_AUTH_MODE=connection_string`; loaded from Key Vault only in that mode |
| `SERVICEBUS_NAMESPACE_FQDN` | empty | Required when `SERVICEBUS_AUTH_MODE=managed_identity`; Service Bus namespace FQDN authenticated through managed identity and RBAC |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
| `SERVICEBUS_DEADLETTER_QUEUE_NAME` | `password-sync-dlq` | Safe DLQ queue name for terminal password sync failures |
| `PASSWORD_MESSAGE_TTL` | `5m` | Queue message TTL and pending sync-status TTL; must be a positive Go duration |
| `SYNC_STATUS_STORE` | empty | Required; `memory` for explicit local/test use or `redis` for production |
| `REDIS_HOST` | empty | Required in Redis mode; Azure Managed Redis host name without scheme or port |
| `REDIS_PORT` | empty | Required in Redis mode; Azure Managed Redis TLS port |
| `REDIS_KEY_PREFIX` | `password-hook:sync-status:` | Non-secret, deployment-neutral prefix for hashed sync-status keys |
| `SYNC_STATUS_TERMINAL_TTL` | `2160h` | Owner-approved `90d` retention for `synced` and `sync_failed` records |
| `AZURE_CLIENT_ID` | empty | Required UUID in Redis mode; selects the runtime UAMI used for Entra authentication |
| `PASSWORD_ENCRYPTION_KEY_B64` | empty | Required; base64-encoded 32-byte AES-GCM key for queued password payloads |
| `PASSWORD_ENCRYPTION_KEY_ID` | `password-payload-key-v1` | Required; key identifier embedded in encrypted queue messages |
| `PORTAL_ALLOWED_CIDRS` | empty | Required; comma-separated allowlist applied to the resolved portal client address |
| `TRUSTED_PROXY_CIDRS` | empty | Required in proxy mode; comma-separated immediate trusted proxy CIDRs established from the approved topology; never portal CIDRs or unrestricted networks |
| `DIRECT_CLIENT_MODE` | `false` | Explicit local/test mode; when `true`, `TRUSTED_PROXY_CIDRS` must be empty; production uses `false` |
| `RATE_LIMIT_PER_IP` | `500` | Optional; defaults to `500`; must be positive when set; per-resolved-client-IP threshold during `RATE_LIMIT_WINDOW` |
| `RATE_LIMIT_WINDOW` | `1s` | Optional; defaults to `1s`; must be a positive Go duration when set; anomaly rate-limit window |
| `HOOK_MAX_BODY_BYTES` | `65536` | Optional; defaults to `65536`; must be a positive byte limit when set; signed hook request body limit |
| `OBSERVABILITY_EXPORTER` | `none` | Optional; set to `azure_monitor` to enable Azure Monitor telemetry export |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; injected automatically by the Azure Container Apps managed OpenTelemetry agent (OTLP gRPC). Never set explicitly in deployment configuration. |
| `AZURE_MONITOR_METRIC_RESOURCE_ID` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; Azure resource ID that owns custom metrics |
| `AZURE_MONITOR_METRIC_REGION` | empty | Required when `OBSERVABILITY_EXPORTER=azure_monitor`; Azure region for the custom metrics endpoint |
| `AZURE_MONITOR_METRIC_NAMESPACE` | `password-hook-service` | Custom metrics namespace when Azure Monitor export is enabled |
