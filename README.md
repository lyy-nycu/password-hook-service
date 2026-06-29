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

Microsoft Graph, Key Vault, worker consumption, retry/DLQ policy, Terraform resources, and CI/CD security gates are implemented in later slices.

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
export HOOK_HMAC_SECRET="local-development-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export PROBLEM_BASE_URL="https://nycu.edu.tw/problems"
export HTTP_ADDR=":8080"
export SERVICEBUS_CONNECTION_STRING="<redacted-send-only-service-bus-connection-string>"
export SERVICEBUS_QUEUE_NAME="password-sync"
```

Use a queue- or topic-level Shared Access Policy with only the `Send` permission for
the producer connection string. Do not use namespace-level manage policies for
application runtime credentials.

Optional local API protection settings:

```bash
export PORTAL_ALLOWED_CIDRS="127.0.0.1/32,::1/128"
export RATE_LIMIT_PER_IP="500"
```

Run the service:

```bash
docker build -f deploy/Dockerfile -t password-hook-service .
docker run --rm -p 8080:8080 \
  -e HOOK_HMAC_SECRET \
  -e ENTRA_PRIMARY_DOMAIN \
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
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOOK_HMAC_SECRET` | empty | HMAC shared secret for portal requests |
| `ENTRA_PRIMARY_DOMAIN` | `nycu.edu.tw` | Domain used to build internal Entra UPNs |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
| `SERVICEBUS_CONNECTION_STRING` | empty | Azure Service Bus connection string used by the producer until Slice 3 moves secret loading to Key Vault |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
| `PORTAL_ALLOWED_CIDRS` | empty | Optional comma-separated source CIDR allowlist |
| `RATE_LIMIT_PER_IP` | `500` | Per-IP request threshold per one-second window |
