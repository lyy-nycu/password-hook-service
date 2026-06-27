# Password Hook Service Slice Roadmap

**Purpose:** Track the service completion strategy at slice level. Each slice is a deployable or independently verifiable increment; detailed task-by-task plans are created one slice at a time.

**Source Design:** `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`

**Planning Rule:** Keep this roadmap high level. Do not expand every slice into code/test steps here. Create a separate detailed implementation plan only for the next active slice.

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
| 8 | Observability | Add operational logs, metrics, and traceability. | Slices 1, 2, 4, 6 | Structured logs include trace IDs; success/failure/skip counters exist; queue/DLQ depth hooks are available for Azure Monitor. |
| 9 | API Protection | Harden ingress for production traffic patterns. | Slice 1 | Portal source allowlist is enforced; anomalous traffic returns 429; non-allowed sources return 401; behavior is documented. |
| 10 | Infrastructure | Implement deployable Azure resources. | Slices 2, 3, 4 | Terraform provisions ACA, Service Bus, Key Vault, ACR, identities, and scaling rules matching the design. |
| 11 | CI/CD and Security Gates | Match the design's pull request and deployment controls. | Infrastructure shape | CI runs tests, vet, gosec, govulncheck, trivy, and gitleaks; CD builds image and supports staging deployment. |
| 12 | Integration and Production Readiness | Validate staging and prepare production operation. | Slices 1-11 | Staging smoke test passes; PHP portal integration guide is verified; alerts, dashboard, DLQ review, rollback, and secret rotation runbooks exist. |

---

## Active Detailed Plan

Current active slice:

- Slice 5: detailed plan not created yet

---

## Slice Boundaries

Slice 1 must not introduce Azure SDK dependencies unless needed for compile-time interfaces. Its job is to make the current HTTP foundation trustworthy.

Slice 2 should focus only on producer-side Service Bus behavior. It should not implement the worker or Graph client.

Slices 4 and 5 can be implemented before the real Graph client by using a processor interface and fake processor tests. This keeps queue lifecycle and retry/DLQ behavior independently verifiable.

Slice 6 should isolate Microsoft Graph API behavior behind a client package and test with HTTP test servers where possible.

Slices 10-12 should happen after the application behavior is stable enough that infrastructure and deployment work has concrete requirements to encode.

---

## Completion Tracking

| Slice | Status | Detailed Plan | Commit/Notes |
|---|---|---|---|
| Project Structure Scaffold | Done | `2026-06-24-password-hook-service-project-structure.md` | `92ba9aa feat: scaffold password hook service foundation` |
| 1. M1 Foundation Hardening | Done | `2026-06-24-m1-foundation-hardening.md` | Review fixes applied locally; full `go test ./... && go vet ./...` passed |
| 2. Producer to Service Bus | Done | `2026-06-25-producer-servicebus.md` | Producer-side Service Bus enqueueing verified and reviewed |
| 3. Secret Loading | Done | `2026-06-26-secret-loading.md` | Key Vault/Managed Identity secret loading verified; explicit local env fallback documented |
| 4. Worker Queue Consumption | Done | `2026-06-27-worker-queue-consumption.md` | Worker receive loop and Service Bus receiver adapter verified with `go test ./...` and `go vet ./...` |
| 5. Retry and DLQ Policy | Not planned | Not created |  |
| 6. Microsoft Graph Client | Not planned | Not created |  |
| 7. Password Data Protection | Not planned | Not created |  |
| 8. Observability | Not planned | Not created |  |
| 9. API Protection | Not planned | Not created |  |
| 10. Infrastructure | Not planned | Not created |  |
| 11. CI/CD and Security Gates | Not planned | Not created |  |
| 12. Integration and Production Readiness | Not planned | Not created |  |
