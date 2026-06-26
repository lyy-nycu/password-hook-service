# Password Hook Service

Password Hook Service is the Phase 1 migration service described in `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`.

It accepts successful LDAP login credentials from the portal, authenticates requests with HMAC-SHA256, skips external-email identities, and enqueues eligible internal student/employee IDs for password sync to Microsoft Entra ID.

## Current Scope

This service currently implements the HTTP foundation and producer-side Azure Service Bus enqueueing:

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

Microsoft Graph, worker consumption, retry/DLQ policy, Terraform resources, and CI/CD security gates are implemented in later slices.

## Local Verification

Run the standard local verification:

```bash
make verify
```

The Makefile wraps the Dockerized Go toolchain. The equivalent raw command is:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."
```

## Local Run

Set the required environment variables:

```bash
export SECRETS_SOURCE="env"
export HOOK_HMAC_SECRET="local-development-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export ENTRA_FALLBACK_DOMAIN="nycu.onmicrosoft.com"
export GRAPH_TENANT_ID="00000000-0000-0000-0000-000000000000"
export GRAPH_CLIENT_ID="11111111-1111-1111-1111-111111111111"
export GRAPH_CLIENT_SECRET="local-graph-client-secret"
export PROBLEM_BASE_URL="https://nycu.edu.tw/problems"
export HTTP_ADDR=":8080"
export SERVICEBUS_CONNECTION_STRING="Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA=="
export SERVICEBUS_QUEUE_NAME="password-sync"
```

Optional local API protection settings:

```bash
export PORTAL_ALLOWED_CIDRS="127.0.0.1/32,::1/128"
export RATE_LIMIT_PER_IP="500"
```

Run the service:

```bash
docker build -f deploy/Dockerfile -t password-hook-service .
docker run --rm -p 8080:8080 \
  -e SECRETS_SOURCE \
  -e HOOK_HMAC_SECRET \
  -e ENTRA_PRIMARY_DOMAIN \
  -e ENTRA_FALLBACK_DOMAIN \
  -e GRAPH_TENANT_ID \
  -e GRAPH_CLIENT_ID \
  -e GRAPH_CLIENT_SECRET \
  -e PROBLEM_BASE_URL \
  -e HTTP_ADDR \
  -e SERVICEBUS_CONNECTION_STRING \
  -e SERVICEBUS_QUEUE_NAME \
  -e PORTAL_ALLOWED_CIDRS \
  -e RATE_LIMIT_PER_IP \
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
  --data '{"cn":"311551001","password":"cleartext_password","displayName":"Test User","mail":"test@nycu.edu.tw"}'
```

The hook endpoint returns `202 Accepted` when the request is accepted by the service. It does not mean the password has already been migrated to Entra ID.

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
| `GRAPH_TENANT_ID` | empty | Microsoft Entra tenant ID for later Graph client use |
| `GRAPH_CLIENT_ID` | empty | App registration client ID for later Graph client use |
| `GRAPH_CLIENT_SECRET` | empty | Graph app client secret when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
| `SERVICEBUS_CONNECTION_STRING` | empty | Azure Service Bus connection string when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
| `PORTAL_ALLOWED_CIDRS` | empty | Optional comma-separated source CIDR allowlist |
| `RATE_LIMIT_PER_IP` | `500` | Per-IP request threshold per one-second window |
