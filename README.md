# Password Hook Service

Password Hook Service is the Phase 1 migration service described in `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`.

It accepts successful LDAP login credentials from the portal, authenticates requests with HMAC-SHA256, skips external-email identities, and enqueues eligible internal student/employee IDs for password sync to Microsoft Entra ID.

## Current Scope

This scaffold implements the M1 foundation:

- Go module and package structure
- `GET /healthz`
- `GET /version`
- `POST /api/v1/hook/password`
- HMAC request-signing middleware
- RFC 9457 problem responses
- password/secret masking helper
- identity classification and UPN building primitives

Azure Service Bus, Microsoft Graph, Key Vault, and Terraform resources are represented by stable package/file boundaries and are implemented in later milestones.

## Local Verification

The workspace currently relies on Docker for the Go toolchain:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./...
```

Run the full formatting and static verification command:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."
```

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOOK_HMAC_SECRET` | empty | HMAC shared secret for portal requests |
| `ENTRA_PRIMARY_DOMAIN` | `nycu.edu.tw` | Domain used to build internal Entra UPNs |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
