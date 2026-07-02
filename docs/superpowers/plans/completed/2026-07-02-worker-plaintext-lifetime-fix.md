# Worker Plaintext Lifetime Fix Implementation Plan

> **Plan Status:** Completed
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the worker's decrypted password `[]byte` to `string` conversion so plaintext can be zeroed before retry backoff, message settlement, or safe DLQ handling.

**Architecture:** Keep the serialized queue schema in `migration.PasswordSyncMessage` unchanged and introduce a worker-local processor command that carries decrypted plaintext as `[]byte`. The worker decrypts once per attempt, passes the same byte slice to the processor, and zeroes it immediately after `ProcessPasswordSync` returns. Tests prove the processor sees the password during the call and that every handed-off buffer is zeroed before retry backoff and settlement.

**Tech Stack:** Go 1.26, existing `internal/worker` retry/DLQ loop, existing `internal/passwordcrypto.ZeroBytes`, fake-based Go tests, Dockerized Go verification.

---

## Context

This plan addresses the blocker from `docs/2026-07-01-security-realignment-review-handover.md`.

Current unsafe flow in `internal/worker/worker.go`:

```go
plaintext, err := w.passwordDecrypter.Decrypt(...)
msg.Password = string(plaintext)
err = w.processor.ProcessPasswordSync(ctx, msg)
msg.Password = ""
passwordcrypto.ZeroBytes(plaintext)
```

The `string(plaintext)` conversion creates an immutable heap copy that cannot be explicitly zeroed. The fix is to remove `msg.Password = string(plaintext)` entirely from the worker path.

## File Structure

- Modify: `internal/worker/worker.go` - add a worker-local byte-oriented processor command and use it in `Processor`.
- Modify: `internal/worker/worker_test.go` - update fake processor helpers and add lifecycle assertions for handed-off plaintext buffers.
- No change: `internal/migration/message.go` - keep queue JSON schema unchanged; `Password string` can remain `json:"-"` for producer-side compatibility tests.
- No change: `internal/servicebusqueue/deadletter.go` - safe DLQ already sanitizes `DeadLetterEntry`; verification still scans it.

---

## Task 1: Add Failing Worker Contract Tests

**Files:**
- Modify: `internal/worker/worker_test.go`

- [x] **Step 1: Update the success test expectation to use processor password bytes**

In `TestWorkerSuccessCompletesAndProcessesDecryptedMessage`, replace the current string password assertion:

```go
if processor.messages[0].Password != "cleartext-password" {
	t.Fatalf("processor Password = %q, want cleartext-password", processor.messages[0].Password)
}
```

with:

```go
if got := string(processor.passwords[0]); got != "cleartext-password" {
	t.Fatalf("processor Password = %q, want cleartext-password", got)
}
```

- [x] **Step 2: Add a settlement lifecycle test**

Add this test after `TestWorkerDoesNotRetainPlaintextDuringRetryBackoff`:

```go
func TestWorkerZerosProcessorPasswordBufferBeforeSettlement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processor := &fakeProcessor{}
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = func() {
		for _, plaintext := range processor.handedOffPasswords {
			if bytes.Contains(plaintext, []byte("cleartext-password")) {
				t.Fatalf("processor password buffer was not cleared before settlement: %q", plaintext)
			}
		}
		cancel()
	}
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, &fakeDeadLetterSink{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}
```

- [x] **Step 3: Strengthen the retry lifecycle test**

In `TestWorkerDoesNotRetainPlaintextDuringRetryBackoff`, add processor buffer checks inside `onSleep`:

```go
for _, plaintext := range processor.handedOffPasswords {
	if bytes.Contains(plaintext, []byte("cleartext-password")) {
		t.Fatalf("processor password buffer was not cleared before retry backoff: %q", plaintext)
	}
}
```

The test setup should declare `processor` before `sleeper`:

```go
processor := &fakeProcessor{errs: []error{errors.New("retry"), nil}}
sleeper := &fakeSleeper{
	onSleep: func() {
		for _, plaintext := range decrypter.returnedPlaintexts {
			if bytes.Contains(plaintext, []byte("cleartext-password")) {
				t.Fatalf("plaintext was not cleared before retry backoff: %q", plaintext)
			}
		}
		for _, plaintext := range processor.handedOffPasswords {
			if bytes.Contains(plaintext, []byte("cleartext-password")) {
				t.Fatalf("processor password buffer was not cleared before retry backoff: %q", plaintext)
			}
		}
	},
}
```

- [x] **Step 4: Update fake processor fields and signature in the test only**

Change the fake processor shape near the bottom of `internal/worker/worker_test.go`:

```go
type fakeProcessor struct {
	calls              int
	messages           []PasswordSyncCommand
	passwords          [][]byte
	handedOffPasswords [][]byte
	err                error
	errs               []error
	afterCall          func()
}

func (p *fakeProcessor) ProcessPasswordSync(ctx context.Context, msg PasswordSyncCommand) error {
	p.calls++
	p.messages = append(p.messages, msg)
	p.passwords = append(p.passwords, append([]byte(nil), msg.Password...))
	p.handedOffPasswords = append(p.handedOffPasswords, msg.Password)
	if p.afterCall != nil {
		p.afterCall()
	}
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return err
	}
	return p.err
}
```

- [x] **Step 5: Run the focused test and confirm failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/worker
```

Expected: FAIL to compile because `PasswordSyncCommand` does not exist and `worker.Processor` still expects `migration.PasswordSyncMessage`.

---

## Task 2: Change Worker Processor Handoff to Bytes

**Files:**
- Modify: `internal/worker/worker.go`

- [x] **Step 1: Add a byte-oriented command type**

Add this type immediately above `type Processor interface`:

```go
type PasswordSyncCommand struct {
	CN          string
	UPN         string
	Password    []byte
	DisplayName string
	Mail        string
	EnqueuedAt  time.Time
}
```

- [x] **Step 2: Change the processor interface**

Replace:

```go
type Processor interface {
	ProcessPasswordSync(context.Context, migration.PasswordSyncMessage) error
}
```

with:

```go
type Processor interface {
	ProcessPasswordSync(context.Context, PasswordSyncCommand) error
}
```

- [x] **Step 3: Replace the string conversion in `processPasswordSync`**

Replace this block:

```go
msg.Password = string(plaintext)
err = w.processor.ProcessPasswordSync(ctx, msg)
msg.Password = ""
passwordcrypto.ZeroBytes(plaintext)
if err == nil {
	return processorResult{attempts: attempts}
}
```

with:

```go
err = w.processPasswordSyncAttempt(ctx, msg, plaintext)
if err == nil {
	return processorResult{attempts: attempts}
}
```

- [x] **Step 4: Add the attempt helper that owns zeroing**

Add this helper below `processPasswordSync`:

```go
func (w *Worker) processPasswordSyncAttempt(ctx context.Context, msg migration.PasswordSyncMessage, plaintext []byte) error {
	defer passwordcrypto.ZeroBytes(plaintext)
	return w.processor.ProcessPasswordSync(ctx, PasswordSyncCommand{
		CN:          msg.CN,
		UPN:         msg.UPN,
		Password:    plaintext,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
		EnqueuedAt:  msg.EnqueuedAt,
	})
}
```

- [x] **Step 5: Run the focused worker tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/worker
```

Expected: PASS.

---

## Task 3: Verify No Worker String Password Handoff Remains

**Files:**
- Modify only if verification finds a missed reference: `internal/worker/worker.go`, `internal/worker/worker_test.go`

- [x] **Step 1: Search for forbidden worker handoff patterns**

Run:

```bash
rg -n 'string\(plaintext\)|msg\.Password\s*=|ProcessPasswordSync\(context\.Context, migration\.PasswordSyncMessage\)|messages\s+\[\]migration\.PasswordSyncMessage' internal/worker
```

Expected: no matches.

- [x] **Step 2: Search for allowed legacy message field usage**

Run:

```bash
rg -n '\.Password|json:"password"|Password:.*json' internal
```

Expected allowed matches:

- `internal/handler/hook.go` request DTO still receives `json:"password"` over HTTPS.
- `internal/migration/message.go` may still define `Password string` with `json:"-"`.
- Producer/app/queue tests may still assert queued `Password` is empty.

Unexpected matches to fix:

- Any worker assignment from decrypted bytes to `Password string`.
- Any Service Bus body/properties containing `password`.
- Any safe DLQ body/properties containing plaintext, ciphertext, nonce, or password fields.

- [x] **Step 3: Keep decrypt failure safe DLQ behavior covered**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/worker -run 'TestWorkerDecryptFailureRecordsSafeDLQWithoutCiphertext|TestWorkerDoesNotRetainPlaintextDuringRetryBackoff|TestWorkerZerosProcessorPasswordBufferBeforeSettlement'
```

Expected: PASS.

---

## Task 4: Full Verification

**Files:**
- No source changes unless verification fails.

- [x] **Step 1: Format**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 gofmt -w internal/worker/worker.go internal/worker/worker_test.go
```

- [x] **Step 2: Run all tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./...
```

Expected: PASS.

- [x] **Step 3: Run vet**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go vet ./...
```

Expected: PASS.

- [x] **Step 4: Run final leak scans**

Run:

```bash
rg -n 'string\(plaintext\)|msg\.Password\s*=|DeadLetterMessage|DeadLetterOptions|password sync message password is required' internal/worker internal/servicebusqueue
rg -n 'json:"password"|Password:.*json' internal
```

Expected:

- First scan has no worker password handoff or native Service Bus DLQ matches.
- Second scan only shows the HTTP hook request DTO and tests/structs that intentionally verify `Password` is not serialized.

---

## Completion Note

Completed 2026-07-02. Verified Tasks 1 and 2 were already implemented: worker processor handoff uses `PasswordSyncCommand` with `Password []byte`, no `string(plaintext)` conversion remains, and retry/settlement tests assert handed-off buffers are zeroed. Verification passed with dockerized focused worker tests, `gofmt`, full `go test ./...`, `go vet ./...`, and final leak scans.

---

## Review Handoff

After implementation, request a fresh review with this checklist:

- `worker.Processor` no longer accepts `migration.PasswordSyncMessage`.
- `internal/worker/worker.go` no longer calls `string(plaintext)`.
- Processor receives password as `[]byte` during the attempt.
- The same plaintext buffer handed to the processor is zeroed before retry backoff and before message settlement.
- Safe DLQ entries still exclude plaintext, ciphertext, nonce, and password fields.
- `go test ./...`, `go vet ./...`, and leak scans pass.
