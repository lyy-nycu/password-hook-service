# Retry and DLQ Policy Implementation Plan

> **Plan Status:** Superseded
>
> **Do Not Execute:** Use `docs/superpowers/plans/completed/2026-07-01-password-payload-encryption-realignment.md` instead.
>
> **Historical Value:** Safe DLQ direction is retained, but worker schema validation, decrypt-per-attempt behavior, and native DLQ removal must be implemented through the realignment plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Slice 5 retry and dead-letter behavior so transient processor failures retry with 1s/2s/4s backoff and terminal failures are written to a password-safe DLQ payload.

**Architecture:** Keep retry orchestration in `internal/worker`, because the worker owns message lifecycle and settlement. Replace worker-path native Service Bus dead-lettering with an application-level safe DLQ sink: write sanitized failure JSON to a dedicated queue, then complete the original password-bearing message so native Service Bus DLQ never stores the original body. Continue using fake processors until Slice 6 provides the real Microsoft Graph client.

**Tech Stack:** Go 1.26, Azure SDK for Go `github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus`, existing fake-based unit tests.

---

## Context

- Roadmap marks Slices 1-4 complete and Slice 5 not yet implemented.
- Git log shows Slice 4 merged via `46388f3 Merge pull request #3 from lyy-nycu/slice-4-worker-queue-consumption`.
- Slice 4 intentionally left production worker wiring out because native Service Bus DLQ preserves original message bodies, including passwords.
- Current worker behavior abandons retryable processor errors once; it does not implement the design's 1s/2s/4s retry policy.
- Current `internal/servicebusqueue.Receiver.DeadLetterMessage` is unsafe for password sync messages because native DLQ stores the original body.

## File Structure

- Modify: `internal/worker/worker.go` - retry policy, safe DLQ contract, safe terminal settlement behavior.
- Modify: `internal/worker/worker_test.go` - tests for safe DLQ, retries, permanent failures, cancellation, sink failure, and leak prevention.
- Create: `internal/servicebusqueue/deadletter.go` - Service Bus sender-backed safe DLQ sink.
- Create: `internal/servicebusqueue/deadletter_test.go` - safe DLQ serialization and send behavior tests.
- Modify: `internal/servicebusqueue/queue.go` - remove native DLQ from the worker receiver path.
- Modify: `internal/servicebusqueue/queue_test.go` - update receiver tests after native DLQ removal.
- Modify: `internal/config/config.go` - add `SERVICEBUS_DEADLETTER_QUEUE_NAME`.
- Modify: `internal/config/config_test.go` - cover dead-letter queue config default and validation.
- Modify after implementation: `docs/superpowers/plans/roadmap.md` - mark Slice 5 done only after verification passes.

## Code Schema And Quality Spec

### Worker Public Contract

`internal/worker/worker.go` must expose these stable types and constants:

```go
const (
	DeadLetterReasonInvalidMessageSchema      = "invalid_message_schema"
	DeadLetterReasonPermanentProcessor        = "permanent_processor_error"
	DeadLetterReasonTransientRetriesExhausted = "transient_processor_retries_exhausted"
)

type Message struct {
	Body []byte
	Kind string
}

type Receiver interface {
	ReceiveMessages(context.Context, int) ([]*Message, error)
	CompleteMessage(context.Context, *Message) error
	AbandonMessage(context.Context, *Message) error
}

type Processor interface {
	ProcessPasswordSync(context.Context, migration.PasswordSyncMessage) error
}

type DeadLetterEntry struct {
	Kind        string    `json:"kind"`
	CN          string    `json:"cn,omitempty"`
	UPN         string    `json:"upn,omitempty"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	Attempts    int       `json:"attempts"`
	EnqueuedAt  time.Time `json:"enqueuedAt,omitempty"`
	FailedAt    time.Time `json:"failedAt"`

	Password string `json:"-"`
}

type DeadLetterSink interface {
	RecordPasswordSyncFailure(context.Context, DeadLetterEntry) error
}

type Options struct {
	MaxMessages       int
	SettlementTimeout time.Duration
	EmptyReceiveDelay time.Duration
	RetryBackoffs     []time.Duration
	DeadLetterSink    DeadLetterSink
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
}
```

### Retry Policy

- Default retry backoffs must be exactly `[]time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}`.
- Transient errors are ordinary non-nil processor errors that do not match `*worker.PermanentError`.
- Total processor attempts for an always-transient failure must be `4`: initial attempt plus 3 retries.
- If a transient attempt succeeds before exhaustion, complete the original message and do not write DLQ.
- If retries exhaust, write one safe DLQ entry with reason `transient_processor_retries_exhausted`, `Attempts: 4`, then complete the original message.
- If retry sleep is canceled, abandon the original message and do not write DLQ.

### Permanent Failure Policy

- `*worker.PermanentError` must skip retry.
- Permanent failures write one safe DLQ entry with reason `permanent_processor_error`, `Attempts: 1`, then complete the original message.
- Do not trust arbitrary `PermanentError.Reason` values from processor code. Map unknown or sensitive reason values to `permanent_processor_error`.

### Invalid Message Policy

- Invalid JSON, wrong message kind, missing `cn`, missing `upn`, missing `password`, or missing `enqueuedAt` writes one safe DLQ entry with reason `invalid_message_schema`.
- Invalid messages must complete the original message after the safe DLQ write succeeds.
- Invalid-message DLQ entries may include parsed `cn`, `upn`, and `enqueuedAt` when those fields can be decoded without trusting the password.

### Safe DLQ Payload

The safe DLQ payload must be JSON and must never include a password field or password value.

Required JSON shape:

```json
{
  "kind": "password-sync",
  "cn": "u1234567",
  "upn": "u1234567@example.edu",
  "reason": "transient_processor_retries_exhausted",
  "description": "transient processor retries exhausted",
  "attempts": 4,
  "enqueuedAt": "2026-06-27T12:00:00Z",
  "failedAt": "2026-06-29T09:00:00Z"
}
```

Do not serialize:

```json
{
  "password": "any value"
}
```

### Settlement Policy

- Success: `CompleteMessage`.
- Transient success after retry: `CompleteMessage`.
- Transient retry exhaustion: write safe DLQ, then `CompleteMessage`.
- Permanent failure: write safe DLQ, then `CompleteMessage`.
- Invalid message: write safe DLQ, then `CompleteMessage`.
- Safe DLQ write failure: `AbandonMessage`, return an error that wraps the safe DLQ error.
- Retry cancellation: `AbandonMessage`, return nil from `Run` when cancellation is the normal shutdown path.

### Quality Constraints

- No password in logs, DLQ body, DLQ application properties, DLQ reason, or DLQ description.
- No native `azservicebus.Receiver.DeadLetterMessage` or `azservicebus.DeadLetterOptions` usage in the password sync worker path.
- Worker remains free of Azure SDK imports.
- Service Bus adapter owns Azure SDK types.
- Tests must use fake receivers/processors/senders; no Azure network dependency.
- Keep the production worker unwired from `cmd/server` in Slice 5.
- Slice 6 owns real Microsoft Graph status-code classification.
- Slice 7 owns password zeroing and memory cleanup.

## Task 1: Add Safe DLQ Contract To Worker

**Files:** modify `internal/worker/worker.go`; modify `internal/worker/worker_test.go`.

- [ ] **Step 1: Write failing tests**
  - Add `TestNewRequiresDeadLetterSink`: `New(receiver, processor, Options{})` returns exactly `worker dead-letter sink is required`.
  - Add `TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal`: a message containing `"password":"secret"` but missing `enqueuedAt` produces zero processor calls, one safe DLQ entry with reason `invalid_message_schema`, one complete, zero abandon, and no `secret` in marshaled `DeadLetterEntry`.

- [ ] **Step 2: Confirm failure**
  - Run: `go test ./internal/worker -run 'TestNewRequiresDeadLetterSink|TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal' -v`
  - Expected: FAIL because `DeadLetterSink` is not implemented and invalid messages still use native dead-lettering.

- [ ] **Step 3: Implement**
  - Add the exact worker schema from `Code Schema And Quality Spec`.
  - Add `var defaultRetryBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}`.
  - `New` must reject nil `Options.DeadLetterSink`, copy `RetryBackoffs`, and default `Now`/`Sleep`.
  - Replace invalid-message native dead-lettering with `DeadLetterSink.RecordPasswordSyncFailure`, then complete the original message with the existing fresh settlement-context pattern.

- [ ] **Step 4: Verify and commit**
  - Run: `go test ./internal/worker -run 'TestNewRequiresDeadLetterSink|TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal' -v`
  - Expected: PASS.
  - Commit: `git add internal/worker/worker.go internal/worker/worker_test.go && git commit -m "feat: require password-safe worker DLQ sink"`

## Task 2: Implement Retry And Terminal Failure Policy

**Files:** modify `internal/worker/worker.go`; modify `internal/worker/worker_test.go`.

- [ ] **Step 1: Write failing retry tests**
  - `TestWorkerRetriesTransientProcessorErrorsBeforeSuccess`: processor returns error, error, nil; sleeper records `[1s, 2s]`; processor calls `3`; complete `1`; abandon `0`; DLQ entries `0`.
  - `TestWorkerRetriesTransientProcessorErrorsThenSafeDLQ`: processor always returns ordinary error; calls `4`; sleeper records `[1s, 2s, 4s]`; one safe DLQ entry with reason `transient_processor_retries_exhausted`, `Attempts: 4`; original completed; password absent from marshaled entry.
  - `TestWorkerPermanentProcessorErrorSkipsRetryAndSafeDLQ`: processor returns `&PermanentError{Reason: PermanentReasonProcessorError, Err: errors.New("graph 403")}`; calls `1`; no sleeps; one safe DLQ entry with reason `permanent_processor_error`, `Attempts: 1`; original completed.
  - `TestWorkerPermanentProcessorErrorDoesNotTrustSensitiveReason`: `PermanentError.Reason` contains UPN and password; safe DLQ reason is exactly `permanent_processor_error`; reason and description contain neither UPN nor password.

- [ ] **Step 2: Confirm failure**
  - Run: `go test ./internal/worker -run 'TestWorkerRetriesTransientProcessorErrorsBeforeSuccess|TestWorkerRetriesTransientProcessorErrorsThenSafeDLQ|TestWorkerPermanentProcessorErrorSkipsRetryAndSafeDLQ|TestWorkerPermanentProcessorErrorDoesNotTrustSensitiveReason' -v`
  - Expected: FAIL because current worker performs one processor attempt.

- [ ] **Step 3: Implement**
  - Add internal retry helper: call `ProcessPasswordSync`; return on nil or `*PermanentError`; sleep between transient failures through `Options.Sleep`; use default backoffs when unset; return terminal transient result after 1s/2s/4s are consumed.
  - Use descriptions: `invalid password sync message`, `permanent processor error`, `transient processor retries exhausted`.
  - For terminal permanent/transient failures, build `DeadLetterEntry` with `CN`, `UPN`, `Reason`, `Description`, `Attempts`, `EnqueuedAt`, `FailedAt`, empty `Password`; write safe DLQ; complete original.

- [ ] **Step 4: Verify and commit**
  - Run the same focused `go test ./internal/worker -run ... -v` command from Step 2.
  - Expected: PASS.
  - Commit: `git add internal/worker/worker.go internal/worker/worker_test.go && git commit -m "feat: add worker retry and terminal failure policy"`

## Task 3: Cover Cancellation And Safe DLQ Failure

**Files:** modify `internal/worker/worker.go`; modify `internal/worker/worker_test.go`.

- [ ] **Step 1: Add tests and fakes**
  - `TestWorkerAbandonsWhenRetryBackoffIsCanceled`: transient error plus fake sleeper returning `context.Canceled`; processor calls `1`; abandon `1`; complete `0`; DLQ entries `0`.
  - `TestWorkerAbandonsOriginalWhenSafeDLQWriteFails`: permanent error plus fake DLQ sink returning `safe DLQ unavailable`; `Run` wraps that error; abandon `1`; complete `0`.
  - Test fakes must include `fakeDeadLetterSink []DeadLetterEntry`, `fakeSleeper []time.Duration`, and `fakeProcessor.errs []error`. Worker receiver fakes must not expose native `DeadLetterMessage`.

- [ ] **Step 2: Verify and commit**
  - Run: `go test ./internal/worker -v`
  - Expected: PASS.
  - Commit: `git add internal/worker/worker.go internal/worker/worker_test.go && git commit -m "test: cover retry cancellation and safe DLQ failure"`

## Task 4: Add Service Bus Safe DLQ Sender

**Files:** create `internal/servicebusqueue/deadletter.go`; create `internal/servicebusqueue/deadletter_test.go`.

- [ ] **Step 1: Write failing Service Bus DLQ tests**
  - `TestDeadLetterQueueSendsSanitizedPasswordSyncFailure`: send `worker.DeadLetterEntry{Password: "must-not-appear"}`; expect subject `password-sync-dlq`, content type `application/json`, properties `kind=password-sync-dlq`, `cn`, `upn`, `reason`; body contains `"attempts":4`; body/properties do not contain `must-not-appear`.
  - `TestNewDeadLetterQueueRejectsNilSender`: error exactly `service bus dead-letter sender is required`.
  - `TestDeadLetterQueueWrapsSendError`: sender returns `service bus send failed`; returned error wraps it and contains `send password sync dead-letter message`.

- [ ] **Step 2: Confirm failure**
  - Run: `go test ./internal/servicebusqueue -run 'TestDeadLetterQueue|TestNewDeadLetterQueue' -v`
  - Expected: FAIL because `NewDeadLetterQueue` does not exist.

- [ ] **Step 3: Implement**
  - Add `const passwordSyncDLQKind = "password-sync-dlq"`.
  - Add `DeadLetterQueue{sender sender, client closer}` implementing `worker.DeadLetterSink`.
  - Provide `NewDeadLetterQueue`, `NewDeadLetterQueueWithClient`, `NewDeadLetterQueueFromConnectionString`, `RecordPasswordSyncFailure`, and `Close`.
  - `RecordPasswordSyncFailure` must set `entry.Password = ""` before marshaling and must exclude password from all `azservicebus.Message.ApplicationProperties`.

- [ ] **Step 4: Verify and commit**
  - Run: `go test ./internal/servicebusqueue -run 'TestDeadLetterQueue|TestNewDeadLetterQueue' -v`
  - Expected: PASS.
  - Commit: `git add internal/servicebusqueue/deadletter.go internal/servicebusqueue/deadletter_test.go && git commit -m "feat: add password-safe service bus DLQ sink"`

## Task 5: Remove Native DLQ From Worker Receiver Path

**Files:** modify `internal/servicebusqueue/queue.go`; modify `internal/servicebusqueue/queue_test.go`; modify `internal/worker/worker_test.go`.

- [ ] **Step 1: Remove native DLQ API**
  - In `internal/servicebusqueue/queue.go`, remove `DeadLetterMessage` from `serviceBusReceiver` and delete `func (r *Receiver) DeadLetterMessage(...)`.
  - Keep `ReceiveMessages`, `CompleteMessage`, `AbandonMessage`, and `Close`.

- [ ] **Step 2: Update tests**
  - In `queue_test.go`, remove native DLQ assertions from `TestReceiverReceivesAndSettlesServiceBusMessage`, `TestReceiverRejectsMessageNotReceivedByReceiver`, and `TestReceiverWithNilNativeReceiverReturnsErrors`.
  - In `worker_test.go`, remove fake native-DLQ fields/methods and assert safe DLQ through `fakeDeadLetterSink`.

- [ ] **Step 3: Verify and commit**
  - Run: `go test ./internal/worker ./internal/servicebusqueue -v`
  - Expected: PASS.
  - Run: `rg -n "DeadLetterMessage|DeadLetterOptions|deadLettered|deadLetterReason|deadLetterDescription" internal`
  - Expected: no results.
  - Commit: `git add internal/worker/worker_test.go internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go && git commit -m "refactor: remove native DLQ from worker receiver path"`

## Task 6: Add DLQ Queue Configuration

**Files:** modify `internal/config/config.go`; modify `internal/config/config_test.go`.

- [ ] **Step 1: Write failing config tests**
  - `TestLoadDefaultsServiceBusDeadLetterQueueName`: `Load().ServiceBusDeadLetterQueueName == "password-sync-dlq"`.
  - `TestValidateRequiresServiceBusDeadLetterQueueName`: empty field returns exactly `SERVICEBUS_DEADLETTER_QUEUE_NAME is required`.

- [ ] **Step 2: Confirm failure**
  - Run: `go test ./internal/config -run 'TestLoadDefaultsServiceBusDeadLetterQueueName|TestValidateRequiresServiceBusDeadLetterQueueName' -v`
  - Expected: FAIL because the config field does not exist.

- [ ] **Step 3: Implement**
  - Add `ServiceBusDeadLetterQueueName string` to `config.Config`.
  - Add `ServiceBusDeadLetterQueueName: env("SERVICEBUS_DEADLETTER_QUEUE_NAME", "password-sync-dlq"),` in `Load`.
  - Add validation: `case strings.TrimSpace(c.ServiceBusDeadLetterQueueName) == "": return errors.New("SERVICEBUS_DEADLETTER_QUEUE_NAME is required")`.

- [ ] **Step 4: Verify and commit**
  - Run: `go test ./internal/config -v`
  - Expected: PASS.
  - Commit: `git add internal/config/config.go internal/config/config_test.go && git commit -m "feat: configure password-safe DLQ queue name"`

## Task 7: Final Verification And Roadmap Update

**Files:** modify `docs/superpowers/plans/roadmap.md`; modify this plan.

- [ ] **Step 1: Run full verification**
  - Run: `go test ./...`
  - Expected: PASS.
  - Run: `go vet ./...`
  - Expected: PASS.

- [ ] **Step 2: Run leak-focused search**
  - Run: `rg -n "password|Password|DeadLetterMessage|DeadLetterOptions" internal/worker internal/servicebusqueue`
  - Expected: password references are limited to password sync processing, test leak assertions, and `json:"-"` on `DeadLetterEntry.Password`; no `DeadLetterMessage` or `DeadLetterOptions` remains in the password sync worker path.

- [ ] **Step 3: Update docs and commit**
  - Update Slice 5 roadmap row only after verification: `| 5. Retry and DLQ Policy | Done | \`superseded/2026-06-29-slice-05-retry-dlq-policy.md\` | Safe DLQ payload excludes password; retry policy verified with \`go test ./...\` and \`go vet ./...\` |`
  - Check off completed tasks in this file.
  - Commit: `git add docs/superpowers/plans/roadmap.md docs/superpowers/plans/superseded/2026-06-29-slice-05-retry-dlq-policy.md && git commit -m "docs: mark retry and DLQ policy slice complete"`

## Out Of Scope

- Real Microsoft Graph HTTP behavior and status-code classification; Slice 6 owns mapping Graph 400/403/429/503/network failures.
- Starting the worker from `cmd/server`; wait until Slice 6 supplies the real processor and Slice 7 completes password memory cleanup.
- Terraform resources for `password-sync-dlq`; Slice 10 owns Azure infrastructure.
- Password zeroing after enqueue/process; Slice 7 owns memory cleanup.

## Self-Review

- Spec coverage: retry, retry exhaustion, permanent failures, invalid messages, password-safe DLQ payloads, and native DLQ removal are covered.
- Schema preservation: worker interfaces, `DeadLetterEntry` JSON schema, DLQ message shape, settlement rules, and config names are explicitly specified.
- Quality preservation: password leak constraints, no Azure SDK in worker, no network tests, `go test ./...`, and `go vet ./...` are retained.
