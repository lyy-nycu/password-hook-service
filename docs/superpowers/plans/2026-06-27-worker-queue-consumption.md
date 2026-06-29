# Worker Queue Consumption Implementation Plan

**Goal:** Consume `password-sync` Service Bus jobs through a worker-owned receiver interface, decode `migration.PasswordSyncMessage`, dispatch to a processor interface, and settle messages according to the processor outcome.

**Architecture:** Keep worker behavior in `internal/worker` with no Azure SDK dependency. Add the Azure Service Bus receiver adapter in `internal/servicebusqueue`, converting native `azservicebus.ReceivedMessage` values into worker messages and mapping worker settlement calls back to native settlement APIs. Do not start the worker from `cmd/server` yet because Graph processing and password-safe DLQ handling are later slices.

**Tech Stack:** Go 1.26, Azure SDK for Go `github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus`, existing fake-based unit tests.

**Security Boundary:** Slice 4 uses native Service Bus dead-lettering, which preserves the original body. The worker must not be enabled in production before slice 5 replaces or constrains DLQ behavior so password payloads are safe.

**Current Status:** Slice 4 implementation and post-handover review fixes are complete locally. The worker remains intentionally unwired from `cmd/server` until Slice 5 provides password-safe DLQ behavior.

---

## File Structure

- Modify: `internal/worker/worker.go` - add receiver, processor, options, permanent error, receive loop, decoding, validation, and settlement behavior.
- Create: `internal/worker/worker_test.go` - cover success, retryable failure, permanent failure, invalid schema, context cancellation, and settlement failures.
- Modify: `internal/servicebusqueue/queue.go` - add receiver adapter construction, receive, complete, abandon, dead-letter, and close behavior while preserving producer APIs.
- Modify: `internal/servicebusqueue/queue_test.go` - cover receiver creation error wrapping, abandon, message ownership errors, nil receiver behavior, and close behavior for receiver plus client.
- Modify: `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md` - point to this plan and record slice 4 completion after verification.

---

## Tasks

- [x] **Task 1: Define worker-facing contracts**
  - Add `worker.Receiver`, `worker.Message`, `worker.Processor`, `worker.Options`, and `worker.PermanentError`.
  - Keep the interface free of Azure SDK types.
  - Validate that worker construction rejects nil receiver or processor.

- [x] **Task 2: Implement receive loop and decoding**
  - Receive batches of password sync messages until context cancellation.
  - Decode JSON into `migration.PasswordSyncMessage`.
  - Reject invalid JSON, wrong message kind, and missing required fields.
  - Treat context cancellation as a normal `Run` exit.

- [x] **Task 3: Implement settlement rules**
  - Processor success calls `CompleteMessage`.
  - Retryable processor error calls `AbandonMessage`.
  - `PermanentError` or invalid schema calls `DeadLetterMessage`.
  - Settlement failures are returned from `Run`.
  - Dead-letter reason and description must be sanitized and must not include the password.

- [x] **Task 4: Add Service Bus receiver adapter**
  - Add `NewReceiverFromConnectionString`.
  - Use `azservicebus.Client.NewReceiverForQueue` with peek-lock receive mode.
  - Implement `ReceiveMessages`, `CompleteMessage`, `AbandonMessage`, `DeadLetterMessage`, and `Close`.
  - Close receiver and client, returning joined close errors.

- [x] **Task 5: Add focused tests**
  - Worker success completes and passes decoded message to processor.
  - Retryable processor error abandons.
  - Permanent processor error dead-letters.
  - Invalid JSON, wrong kind, and missing required fields dead-letter.
  - Context cancellation exits without processing more messages.
  - Settlement failures return from `Run`.
  - Service Bus receiver creation errors are wrapped and close closes receiver/client.

- [x] **Task 6: Verify**
  - Run `go test ./...`.
  - Run `go vet ./...`.
  - Update this checklist and roadmap status after verification.

- [x] **Task 7: Address post-handover review findings**
  - Treat permanent processor DLQ reasons as fixed enum-like values instead of trusting arbitrary `PermanentError.Reason` text.
  - Add regression coverage proving a password or UPN in `PermanentError.Reason` is not written to DLQ metadata.
  - Settle messages with a short fresh settlement context after processor return so shutdown cancellation does not leave a successfully processed message unsettled.
  - Add regression coverage for settlement after processor-side cancellation.
  - Add Service Bus receiver adapter coverage for `AbandonMessage`, settling a worker message not received by the receiver, and nil receiver behavior.

---

## Out Of Scope

- Real Microsoft Graph client behavior.
- Retry backoff and final DLQ policy.
- Password-safe replacement payloads for dead-lettered jobs.
- Starting a production worker in `cmd/server`.
- Fixing slice 3 Graph secret-loading gaps unless they block compilation.
