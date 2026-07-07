# Password Hook Service Slice Roadmap

**Purpose:** Track the service completion strategy at slice level. Each slice is a deployable or independently verifiable increment; detailed task-by-task plans are created one slice at a time.

**Source Design:** `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`

**Planning Rule:** Keep this roadmap high level. Do not expand every slice into code/test steps here. Create a separate detailed implementation plan only for the next active slice.

**Agent Entry Point:** Read `docs/superpowers/plans/README.md` before choosing a detailed plan.

---

## Slice Sequence

| Slice | Name | Goal | Depends On | Done Criteria |
|---|---|---|---|---|
| 1 | M1 Foundation Hardening | Finish the HTTP foundation before Azure integrations. | Project structure scaffold | Config validation, request ID propagation, HMAC hardening, RFC 9457 consistency, log masking, rate/source protection, route tests, local usage docs. |
| 2 | Producer to Service Bus | Make the hook endpoint enqueue eligible password sync jobs. | Slice 1 | Internal student/employee IDs enqueue to Azure Service Bus with TTL 300s; external emails skip without enqueue; password is not logged. |
| 3 | Secret Loading | Load runtime secrets through Azure Key Vault and Managed Identity. | Slice 2 interface shape | HMAC secret, Service Bus connection, Graph credentials, and tenant config load without hardcoded secrets; local dev fallback is explicit. |
| 4 | Worker Queue Consumption | Consume password sync jobs and drive a processor interface. | Slice 2 | Worker receives messages, deserializes schema, acks success, abandons retryable failures, dead-letters permanent failures. |
| 5 | Retry and DLQ Policy | Implement the failure policy from the design. | Slice 4 | Transient Graph-like failures retry with 1s/2s/4s backoff; permanent failures go to DLQ; DLQ payload excludes password. |
| 6 | Microsoft Graph Client | Create or update Entra users and passwords. | Slice 4 | Existing users are patched; missing users are created; Graph 400/403/429/503/network errors classify correctly. |
| 7 | Password Data Protection | Enforce no persistence and memory cleanup behavior. | Slices 2, 4, 6 | Password fields are zeroed after enqueue/process; logs and DLQ never contain password; tests cover leak-prone paths. |
| 7A | Portal Password Event Semantics and Sync Status | Replace the "every successful login" story with explicit `login_bootstrap`/`password_change`/`password_recovery` events and a worker-owned sync-status model. | Slices 2, 4, 6, 7 | API accepts an event type; `login_bootstrap` no-ops once an account is worker-confirmed `synced`; `password_change`/`password_recovery` always enqueue; synced state is only set after Graph success; leak-focused tests still pass. |
| 8 | Observability | Add operational logs, metrics, and traceability. | Slices 1, 2, 4, 6, 7A | Structured logs include trace IDs; success/failure/skip counters exist; queue/DLQ depth hooks are available for Azure Monitor; metrics include event-type and sync-status labels per Slice 7A. |
| 9 | API Protection | Harden ingress for production traffic patterns. | Slice 1 | Portal source allowlist is enforced; anomalous traffic returns 429; non-allowed sources return 401; behavior is documented. |
| 10 | Infrastructure | Implement deployable Azure resources. | Slices 2, 3, 4 | Terraform provisions ACA, Service Bus, Key Vault, ACR, identities, and scaling rules matching the design. |
| 11 | CI/CD and Security Gates | Match the design's pull request and deployment controls. | Infrastructure shape | CI runs tests, vet, gosec, govulncheck, trivy, and gitleaks; CD builds image and supports staging deployment. |
| 12 | Integration and Production Readiness | Validate staging and prepare production operation. | Slices 1-11 | Staging smoke test passes; PHP portal integration guide is verified; alerts, dashboard, DLQ review, rollback, and secret rotation runbooks exist. |

---

## Active Detailed Plan

No plan is currently active. Slice 7A (Portal Password Event Semantics and Sync Status) is complete; see `completed/2026-07-04-slice-07a-portal-password-event-sync-status.md`. Promote the next slice from `drafts/` once its assumptions are refreshed against the Slice 7A event/sync-status model (see Slice Boundaries below).

---

## Slice Boundaries

Slice 1 must not introduce Azure SDK dependencies unless needed for compile-time interfaces. Its job is to make the current HTTP foundation trustworthy.

Slice 2 should focus only on producer-side Service Bus behavior. It should not implement the worker or Graph client.

Slices 4 and 5 can be implemented before the real Graph client by using a processor interface and fake processor tests. This keeps queue lifecycle and retry/DLQ behavior independently verifiable.

Slice 6 should isolate Microsoft Graph API behavior behind a client package and test with HTTP test servers where possible.

Slices 10-12 should happen after the application behavior is stable enough that infrastructure and deployment work has concrete requirements to encode.

Slice 7A must land before Slice 8, 9, 10, 11, or 12 are promoted from draft to active, because those drafts assume the old "every successful login" story and need refreshing once the event/sync-status semantics are confirmed.

---

## Completion Tracking

| Slice | Status | Detailed Plan | Commit/Notes |
|---|---|---|---|
| Project Structure Scaffold | Done | `completed/2026-06-24-project-structure.md` | `92ba9aa feat: scaffold password hook service foundation` |
| 1. M1 Foundation Hardening | Done | `completed/2026-06-24-slice-01-m1-foundation-hardening.md` | Review fixes applied locally; full `go test ./... && go vet ./...` passed |
| 2. Producer to Service Bus | Completed / Partially Superseded | `completed/2026-06-25-slice-02-producer-servicebus.md` | Producer-side Service Bus patterns remain useful; plaintext queue schema superseded by Security Realignment |
| 3. Secret Loading | Completed / Partially Superseded | `completed/2026-06-26-slice-03-secret-loading.md` | Key Vault/Managed Identity patterns remain useful; password payload encryption key loading added by Security Realignment |
| 4. Worker Queue Consumption | Completed / Partially Superseded | `completed/2026-06-27-slice-04-worker-queue-consumption.md` | Worker loop and receiver adapter patterns remain useful; plaintext decode/native DLQ assumptions superseded by Security Realignment |
| Security Realignment | Done | `completed/2026-07-01-password-payload-encryption-realignment.md` | Queue payloads encrypted before enqueue; worker decrypts per attempt; native DLQ removed from password sync path; verified with `go test ./...`, `go vet ./...`, and leak-focused `rg` scans |
| Worker Plaintext Lifetime Fix | Done | `completed/2026-07-02-worker-plaintext-lifetime-fix.md` | Verified dockerized focused worker tests, `gofmt`, full `go test ./...`, `go vet ./...`, and leak scans passed |
| 5. Retry and DLQ Policy | Superseded | `superseded/2026-06-29-slice-05-retry-dlq-policy.md` | Do not execute; safe DLQ intent retained in Security Realignment |
| 6. Microsoft Graph Client | Done | `completed/2026-07-02-slice-06-microsoft-graph-client.md` | Existing users patch, missing users create, and Graph failure classification implemented; verified with focused package tests, full `go test ./...`, `go vet ./...`, and leak-focused `rg` scans |
| 7. Password Data Protection | Done | `completed/2026-07-03-slice-07-password-data-protection.md` | Producer plaintext decoded into mutable buffers and zeroed on all paths; worker and Graph buffers covered by cleanup tests; log masking guards password/secret/token variants; verified with focused tests, full `go test ./...`, `go vet ./...`, and leak-focused `rg` scans |
| 7A. Portal Password Event Semantics and Sync Status | Done | `completed/2026-07-04-slice-07a-portal-password-event-sync-status.md` | `eventType` added to hook request and wire message; `login_bootstrap` skipped once synced or pending within TTL; `password_change`/`password_recovery` always enqueue; worker records `synced`/`sync_failed` after Graph outcome using an ordering guard against out-of-order completions; verified with focused package tests, full `go test ./... -race`, `go vet ./...`, and leak-focused `rg` scans |
| 8. Observability | Not planned | Not created |  |
| 9. API Protection | Not planned | Not created |  |
| 10. Infrastructure | Not planned | Not created |  |
| 11. CI/CD and Security Gates | Not planned | Not created |  |
| 12. Integration and Production Readiness | Not planned | Not created |  |
