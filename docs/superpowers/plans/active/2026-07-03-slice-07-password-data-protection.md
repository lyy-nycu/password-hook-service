# Slice 7 Password Data Protection Implementation Plan

> **Plan Status:** Active
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining password data protection gaps after Slice 6 so plaintext passwords are mutable, zeroed on every producer/worker path, and guarded by focused leak tests.

**Architecture:** Keep the encrypted Service Bus schema, safe DLQ schema, and Microsoft Graph behavior unchanged. Move producer-side plaintext from immutable `string` values to borrowed mutable `[]byte` buffers from HTTP JSON decode through `migration.Service.Submit`, then zero those buffers on every success, skip, and error path. Strengthen worker message-body cleanup and log masking so password material is not retained or emitted beyond the minimum call scope.

**Tech Stack:** Go 1.26, standard-library `encoding/json`, `io`, `unicode/utf16`, `unicode/utf8`, existing `internal/passwordcrypto.ZeroBytes`, existing fake-based tests, existing `go test ./...` and `go vet ./...` verification.

---

## Scope

This plan implements Slice 7 only.

In scope:

- Remove producer-side immutable plaintext password strings from `handler` -> `migration` handoff.
- Zero HTTP request-body bytes and decoded password bytes on validation, skip, enqueue success, encryption failure, queue failure, and hook error paths.
- Keep worker decrypted plaintext as borrowed `[]byte` and add tests that worker message bodies are zeroed before success, abandon, and safe-DLQ settlement.
- Add regression tests that Graph request body buffers are zeroed on error as well as success.
- Expand structured-log masking to cover password/secret/token key variants such as `passwordCiphertext`, `GRAPH_CLIENT_SECRET`, and `access_token`.
- Update docs and roadmap status when implementation is complete.

Out of scope:

- Changing the encrypted queue payload schema or the safe DLQ record schema.
- Removing Service Bus ciphertext persistence; Slice 7 protects plaintext and leak-prone memory/log/DLQ paths, while encrypted queue persistence is intentional.
- Adding metrics or trace dashboards; Slice 8 owns observability.
- Terraform, managed identity, and App Registration changes; Slice 10 owns infrastructure.

## Current Context

- Slice 6 is complete on base commit `2d0706f30cf47d6fa9faf3e2e80996f1cf21d4bc`.
- `internal/migration/service.go` encrypts before enqueue and zeroes a local `[]byte(req.Password)`, but `migration.Request.Password` is still an immutable `string`.
- `internal/handler/hook.go` decodes JSON directly into `passwordHookRequest.Password string`, so the producer path still creates immutable plaintext copies.
- `internal/worker/worker.go` already passes decrypted passwords as borrowed `[]byte` and zeroes them after each processor attempt.
- `internal/worker/worker.go` zeroes invalid message bodies early; success and terminal decoded-message paths rely on receiver settlement behavior rather than worker-level cleanup.
- `internal/graphclient/client.go` zeroes mutable Graph request bodies with `defer passwordcrypto.ZeroBytes(body)`; tests cover success but not Graph error paths.
- `pkg/logger/logger.go` masks exact keys `password`, `passwd`, and `secret`, but not common variants like `passwordCiphertext`, `clientSecret`, or `access_token`.

## File Structure

- Modify: `internal/migration/service.go` - make `Request.Password` a borrowed mutable `[]byte` and zero it with `defer` on all `Submit` paths.
- Modify: `internal/migration/service_test.go` - add producer zeroing tests for success, external skip, encryption failure, and queue failure.
- Modify: `internal/handler/hook.go` - read request bodies into a mutable buffer, decode password JSON strings into `[]byte`, and zero raw/decoded buffers.
- Modify: `internal/handler/hook_test.go` - add hook-level zeroing and escaped-password decode tests; update fakes for byte passwords.
- Modify: `internal/worker/worker.go` - zero worker message bodies before complete, abandon, and safe-DLQ recording on decoded-message paths.
- Modify: `internal/worker/worker_test.go` - add worker message-body cleanup assertions for success, retry cancellation, and terminal safe DLQ.
- Modify: `internal/graphclient/client_test.go` - add Graph request-body zeroing regression tests for Graph error responses.
- Modify: `pkg/logger/logger.go` - broaden sensitive-key detection.
- Modify: `pkg/logger/logger_test.go` - add masking tests for password/secret/token key variants.
- Modify: `README.md` - document mutable plaintext lifetime and zeroing behavior.
- Modify: `docs/superpowers/plans/README.md` - update active/completed pointer at the end of implementation.
- Modify: `docs/superpowers/plans/roadmap.md` - mark Slice 7 active during implementation and completed when done.
- Move when complete: `docs/superpowers/plans/active/2026-07-03-slice-07-password-data-protection.md` -> `docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md`.

---

## Task 1: Make Migration Password Input Mutable and Owned

**Files:**
- Modify: `internal/migration/service.go`
- Modify: `internal/migration/service_test.go`

- [ ] **Step 1: Add failing service zeroing tests**

In `internal/migration/service_test.go`, add `errors` to the imports:

```go
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)
```

Add these tests and helpers after `TestServiceEncryptsPasswordBeforeEnqueue`:

```go
func TestServiceZerosPasswordAfterSuccessfulEnqueue(t *testing.T) {
	t.Parallel()

	password := []byte("cleartext-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	assertZeroedBytes(t, password, "request password after successful enqueue")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after successful enqueue")
}

func TestServiceZerosPasswordWhenSkippingExternalEmail(t *testing.T) {
	t.Parallel()

	password := []byte("external-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)

	decision, err := service.Submit(context.Background(), Request{
		CN:          "guest@gmail.com",
		Password:    password,
		DisplayName: "Guest",
		Mail:        "guest@gmail.com",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Skipped {
		t.Fatal("decision.Skipped = false, want true")
	}
	if len(encrypter.password) != 0 {
		t.Fatalf("encrypter was called for skipped external identity")
	}
	assertZeroedBytes(t, password, "request password after external skip")
}

func TestServiceZerosPasswordWhenEncryptFails(t *testing.T) {
	t.Parallel()

	password := []byte("encrypt-failure-password")
	encryptErr := errors.New("encrypt failed")
	service := NewService("nycu.edu.tw", &captureQueue{}, failingEncrypter{err: encryptErr})

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, encryptErr) {
		t.Fatalf("Submit error = %v, want encrypt error", err)
	}
	assertZeroedBytes(t, password, "request password after encrypt failure")
}

func TestServiceZerosPasswordWhenQueueFails(t *testing.T) {
	t.Parallel()

	password := []byte("queue-failure-password")
	queueErr := errors.New("queue unavailable")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", failingQueue{err: queueErr}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, queueErr) {
		t.Fatalf("Submit error = %v, want queue error", err)
	}
	assertZeroedBytes(t, password, "request password after queue failure")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after queue failure")
}

type captureEncrypter struct {
	password []byte
}

func (e *captureEncrypter) Encrypt(_ context.Context, password []byte, _ []byte) (passwordcrypto.Envelope, error) {
	e.password = password
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}

type failingEncrypter struct {
	err error
}

func (e failingEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{}, e.err
}

type failingQueue struct {
	err error
}

func (q failingQueue) EnqueuePasswordSync(context.Context, PasswordSyncMessage) error {
	return q.err
}

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run:

```bash
go test ./internal/migration -run 'TestServiceZerosPassword' -v
```

Expected: FAIL to compile because `migration.Request.Password` is still a `string` and the new tests pass `[]byte`.

- [ ] **Step 3: Change the migration request contract**

In `internal/migration/service.go`, replace the `Request` type with this version:

```go
type Request struct {
	CN          string
	Password    []byte
	DisplayName string
	Mail        string
}
```

Add this comment immediately above the `Password` field:

```go
	// Password is borrowed mutable memory. Submit zeroes it before returning on
	// every success, skip, and error path; callers must not reuse it afterward.
	Password []byte
```

The full struct should be:

```go
type Request struct {
	CN string
	// Password is borrowed mutable memory. Submit zeroes it before returning on
	// every success, skip, and error path; callers must not reuse it afterward.
	Password    []byte
	DisplayName string
	Mail        string
}
```

- [ ] **Step 4: Zero borrowed password bytes on every Submit path**

In `Submit`, add the zeroing defer before identity classification:

```go
func (s *Service) Submit(ctx context.Context, req Request) (Decision, error) {
	defer passwordcrypto.ZeroBytes(req.Password)

	identityType := ClassifyCN(req.CN)
	decision := Decision{IdentityType: identityType}
```

Replace the local string-to-byte conversion:

```go
	passwordBytes := []byte(req.Password)
	defer passwordcrypto.ZeroBytes(passwordBytes)
	env, err := s.encrypter.Encrypt(ctx, passwordBytes, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
```

with:

```go
	env, err := s.encrypter.Encrypt(ctx, req.Password, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
```

- [ ] **Step 5: Update existing migration tests to pass byte passwords**

In `TestServiceEncryptsPasswordBeforeEnqueue`, replace:

```go
Password:    "cleartext-password",
```

with:

```go
Password:    []byte("cleartext-password"),
```

- [ ] **Step 6: Run focused migration tests**

Run:

```bash
gofmt -w internal/migration/service.go internal/migration/service_test.go
go test ./internal/migration -v
```

Expected: PASS.

- [ ] **Step 7: Commit migration ownership changes**

Run:

```bash
git add internal/migration/service.go internal/migration/service_test.go
git commit -m "fix: zero migration password buffers"
```

---

## Task 2: Decode Hook Passwords Into Mutable Bytes

**Files:**
- Modify: `internal/handler/hook.go`
- Modify: `internal/handler/hook_test.go`

- [ ] **Step 1: Add failing hook password lifetime tests**

In `internal/handler/hook_test.go`, add `strings` to the imports if it is not already present.

Add these tests after `TestHookEnqueuesInternalStudentID`:

```go
func TestHookZerosDecodedPasswordAfterSubmit(t *testing.T) {
	t.Parallel()

	encrypter := &capturePasswordEncrypter{}
	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, encrypter)
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"cleartext-password","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(encrypter.password) == 0 {
		t.Fatal("encrypter did not see password bytes")
	}
	assertZeroedBytes(t, encrypter.password, "decoded hook password after submit")
}

func TestHookDecodesEscapedPasswordIntoBytes(t *testing.T) {
	t.Parallel()

	var body passwordHookRequest
	err := json.Unmarshal([]byte(`{"cn":"311551001","password":"pa\"ss\nword","displayName":"Student","mail":"student@nycu.edu.tw"}`), &body)

	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got := string(body.Password); got != "pa\"ss\nword" {
		t.Fatalf("password = %q, want escaped password decoded", got)
	}
	passwordcrypto.ZeroBytes(body.Password)
	assertZeroedBytes(t, body.Password, "decoded escaped password")
}

func TestHookInvalidJSONDoesNotEchoPassword(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", strings.NewReader(`{"cn":"311551001","password":"cleartext-password"`))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if strings.Contains(rec.Body.String(), "cleartext-password") {
		t.Fatalf("problem response leaked password: %s", rec.Body.String())
	}
}
```

Add this fake near the existing `fakePasswordEncrypter`:

```go
type capturePasswordEncrypter struct {
	password []byte
}

func (e *capturePasswordEncrypter) Encrypt(_ context.Context, password []byte, _ []byte) (passwordcrypto.Envelope, error) {
	e.password = password
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}
```

- [ ] **Step 2: Update existing hook tests for byte passwords**

In `internal/handler/hook_test.go`, keep request JSON unchanged. Do not change `fakePasswordEncrypter`; it already accepts `[]byte`, so it should remain:

```go
func (fakePasswordEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}
```

- [ ] **Step 3: Run focused hook tests and confirm failure**

Run:

```bash
go test ./internal/handler -run 'TestHook' -v
```

Expected: FAIL because `passwordHookRequest.Password` is still a `string`, the handler streams JSON directly from `r.Body`, and `migration.Request.Password` now expects `[]byte`.

- [ ] **Step 4: Read and zero raw request bodies**

In `internal/handler/hook.go`, add imports:

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)
```

In `ServeHTTP`, replace:

```go
	var body passwordHookRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be valid json"))
		return
	}
```

with:

```go
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be readable"))
		return
	}
	defer passwordcrypto.ZeroBytes(rawBody)

	var body passwordHookRequest
	if err := json.Unmarshal(rawBody, &body); err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be valid json"))
		return
	}
	defer passwordcrypto.ZeroBytes(body.Password)
```

In the `migration.Request` construction, replace:

```go
Password:    body.Password,
```

with:

```go
Password:    []byte(body.Password),
```

- [ ] **Step 5: Change the hook request password field to custom mutable bytes**

Replace the request struct:

```go
type passwordHookRequest struct {
	CN          string `json:"cn"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Mail        string `json:"mail"`
}
```

with:

```go
type passwordHookRequest struct {
	CN          string        `json:"cn"`
	Password    passwordBytes `json:"password"`
	DisplayName string        `json:"displayName"`
	Mail        string        `json:"mail"`
}
```

Keep validation byte-oriented:

```go
	case len(r.Password) == 0:
		return "Field 'password' is required"
```

- [ ] **Step 6: Add the JSON string byte decoder**

Add this code below `passwordHookRequest.validate` in `internal/handler/hook.go`:

```go
type passwordBytes []byte

func (p *passwordBytes) UnmarshalJSON(data []byte) error {
	decoded, err := decodeJSONStringBytes(data)
	if err != nil {
		return err
	}
	*p = decoded
	return nil
}

func decodeJSONStringBytes(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return nil, errors.New("password must be a json string")
	}

	out := make([]byte, 0, len(data)-2)
	for i := 1; i < len(data)-1; i++ {
		b := data[i]
		if b != '\\' {
			if b < 0x20 {
				return nil, errors.New("password contains invalid json string control character")
			}
			out = append(out, b)
			continue
		}

		i++
		if i >= len(data)-1 {
			return nil, errors.New("password contains invalid json escape")
		}
		switch data[i] {
		case '"', '\\', '/':
			out = append(out, data[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, consumed, err := decodeUnicodeEscape(data[i+1 : len(data)-1])
			if err != nil {
				return nil, err
			}
			i += consumed
			out = utf8.AppendRune(out, r)
		default:
			return nil, fmt.Errorf("password contains invalid json escape %q", data[i])
		}
	}
	return out, nil
}

func decodeUnicodeEscape(data []byte) (rune, int, error) {
	if len(data) < 4 {
		return 0, 0, errors.New("password contains short unicode escape")
	}
	r, err := hex4(data[:4])
	if err != nil {
		return 0, 0, err
	}
	if !utf16.IsSurrogate(r) {
		return r, 4, nil
	}
	if r < 0xD800 || r > 0xDBFF {
		return 0, 0, errors.New("password contains invalid unicode surrogate")
	}
	if len(data) < 10 || data[4] != '\\' || data[5] != 'u' {
		return 0, 0, errors.New("password contains unmatched unicode surrogate")
	}
	low, err := hex4(data[6:10])
	if err != nil {
		return 0, 0, err
	}
	decoded := utf16.DecodeRune(r, low)
	if decoded == utf8.RuneError {
		return 0, 0, errors.New("password contains invalid unicode surrogate pair")
	}
	return decoded, 10, nil
}

func hex4(data []byte) (rune, error) {
	var r rune
	for _, b := range data {
		r <<= 4
		switch {
		case b >= '0' && b <= '9':
			r += rune(b - '0')
		case b >= 'a' && b <= 'f':
			r += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			r += rune(b-'A') + 10
		default:
			return 0, fmt.Errorf("password contains invalid unicode escape byte %q", b)
		}
	}
	return r, nil
}
```

- [ ] **Step 7: Run focused hook tests**

Run:

```bash
gofmt -w internal/handler/hook.go internal/handler/hook_test.go
go test ./internal/handler -v
```

Expected: PASS.

- [ ] **Step 8: Commit hook byte decoding changes**

Run:

```bash
git add internal/handler/hook.go internal/handler/hook_test.go
git commit -m "fix: decode hook passwords into mutable buffers"
```

---

## Task 3: Zero Worker Message Bodies Before Settlement

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Add failing worker message-body cleanup tests**

In `internal/worker/worker_test.go`, add these tests after `TestWorkerZerosProcessorPasswordBufferBeforeSettlement`:

```go
func TestWorkerZerosMessageBodyBeforeSuccessSettlement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := workerMessage(t, validPasswordSyncMessage())
	receiver := &fakeReceiver{messages: []*Message{msg}}
	receiver.onComplete = func() {
		assertZeroedPasswordBuffer(t, msg.Body, "worker message body before success settlement")
		cancel()
	}
	worker := newTestWorker(t, receiver, &fakeProcessor{}, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, &fakeDeadLetterSink{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestWorkerZerosMessageBodyBeforeTerminalSafeDLQ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := workerMessage(t, validPasswordSyncMessage())
	receiver := &fakeReceiver{messages: []*Message{msg}}
	receiver.onComplete = cancel
	deadLetters := &fakeDeadLetterSink{
		onRecord: func() {
			assertZeroedPasswordBuffer(t, msg.Body, "worker message body before terminal safe DLQ")
		},
	}
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReasonProcessorError,
		Err:    errors.New("graph 403"),
	}}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, deadLetters, &fakeSleeper{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestWorkerZerosMessageBodyBeforeRetryCancelAbandon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := workerMessage(t, validPasswordSyncMessage())
	receiver := &fakeReceiver{messages: []*Message{msg}}
	receiver.onAbandon = func() {
		assertZeroedPasswordBuffer(t, msg.Body, "worker message body before retry-cancel abandon")
		cancel()
	}
	processor := &fakeProcessor{err: errors.New("graph temporarily unavailable")}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, &fakeDeadLetterSink{}, &fakeSleeper{err: context.Canceled})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run focused worker cleanup tests and confirm failure**

Run:

```bash
go test ./internal/worker -run 'TestWorkerZerosMessageBody' -v
```

Expected: FAIL because decoded-message success, retry-cancel, and terminal failure paths do not zero `msg.Body` before receiver callbacks run.

- [ ] **Step 3: Zero decoded message bodies before settlement operations**

In `internal/worker/worker.go`, inside `processMessage`, add `zeroMessageBody(msg)` before each decoded-message settlement path.

For success, replace:

```go
	if result.err == nil {
		settleCtx, cancel := w.settlementContext()
```

with:

```go
	if result.err == nil {
		zeroMessageBody(msg)
		settleCtx, cancel := w.settlementContext()
```

For retry cancellation, replace:

```go
	if result.retryCanceled {
		settleCtx, cancel := w.settlementContext()
```

with:

```go
	if result.retryCanceled {
		zeroMessageBody(msg)
		settleCtx, cancel := w.settlementContext()
```

For terminal safe DLQ, replace:

```go
	settleCtx, cancel := w.settlementContext()
	defer cancel()
	if settleErr := w.recordPasswordSyncFailure(settleCtx, DeadLetterEntry{
```

with:

```go
	zeroMessageBody(msg)
	settleCtx, cancel := w.settlementContext()
	defer cancel()
	if settleErr := w.recordPasswordSyncFailure(settleCtx, DeadLetterEntry{
```

- [ ] **Step 4: Run focused worker tests**

Run:

```bash
gofmt -w internal/worker/worker.go internal/worker/worker_test.go
go test ./internal/worker -v
```

Expected: PASS.

- [ ] **Step 5: Commit worker message cleanup changes**

Run:

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "fix: zero worker message bodies before settlement"
```

---

## Task 4: Strengthen Graph and Log Leak Guards

**Files:**
- Modify: `internal/graphclient/client_test.go`
- Modify: `pkg/logger/logger.go`
- Modify: `pkg/logger/logger_test.go`

- [ ] **Step 1: Add Graph request-body zeroing regression tests for error responses**

In `internal/graphclient/client_test.go`, add this test after `TestUpsertUserPasswordClearsRequestBodyBufferAfterAttempt`:

```go
func TestUpsertUserPasswordClearsRequestBodyBufferAfterGraphError(t *testing.T) {
	tests := []struct {
		name       string
		lookupCode int
		writeCode  int
	}{
		{name: "patch permanent error", lookupCode: http.StatusOK, writeCode: http.StatusBadRequest},
		{name: "patch transient error", lookupCode: http.StatusOK, writeCode: http.StatusServiceUnavailable},
		{name: "create permanent error", lookupCode: http.StatusNotFound, writeCode: http.StatusBadRequest},
		{name: "create transient error", lookupCode: http.StatusNotFound, writeCode: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []byte
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					w.WriteHeader(tt.lookupCode)
					return
				}
				w.WriteHeader(tt.writeCode)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, func(body []byte) {
				captured = body
			})

			err := client.UpsertUserPassword(context.Background(), User{
				UPN:         "311551001@nycu.edu.tw",
				DisplayName: "Student One",
				Mail:        "student@nycu.edu.tw",
			}, []byte("cleartext-password"))

			if err == nil {
				t.Fatal("UpsertUserPassword returned nil error")
			}
			if len(captured) == 0 {
				t.Fatal("AfterRequestBodyBuilt did not capture a body")
			}
			if bytes.Contains(captured, []byte("cleartext-password")) {
				t.Fatalf("request body buffer still contains password after graph error: %q", captured)
			}
		})
	}
}
```

- [ ] **Step 2: Add logger key-variant masking tests**

In `pkg/logger/logger_test.go`, extend `TestMaskAttrsMasksSensitiveKeys` with these attrs:

```go
slog.String("passwordCiphertext", "ciphertext"),
slog.String("password_nonce", "nonce"),
slog.String("GRAPH_CLIENT_SECRET", "client-secret"),
slog.String("clientSecret", "client-secret"),
slog.String("access_token", "token"),
```

Extend the masked-key loop:

```go
for _, key := range []string{"password", "passwd", "secret", "passwordCiphertext", "password_nonce", "GRAPH_CLIENT_SECRET", "clientSecret", "access_token"} {
	if values[key] != "****" {
		t.Fatalf("%s = %q, want masked value", key, values[key])
	}
}
```

Add a non-sensitive assertion so HMAC nonce logs remain useful:

```go
if values["cn"] != "311551001" {
	t.Fatalf("cn = %q, want original value", values["cn"])
}
if values["requestNonce"] != "nonce-for-replay-defense" {
	t.Fatalf("requestNonce = %q, want nonce preserved", values["requestNonce"])
}
```

Set `requestNonce` in the attrs:

```go
slog.String("requestNonce", "nonce-for-replay-defense"),
```

- [ ] **Step 3: Run focused graph/logger tests and confirm logger failure**

Run:

```bash
go test ./internal/graphclient ./pkg/logger -v
```

Expected: `internal/graphclient` may already PASS because request body zeroing exists; `pkg/logger` FAILS because sensitive key detection only masks exact keys.

- [ ] **Step 4: Broaden sensitive key detection**

In `pkg/logger/logger.go`, replace `isSensitiveKey`:

```go
func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "password", "passwd", "secret":
		return true
	default:
		return false
	}
}
```

with:

```go
func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	return strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "passwd") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token")
}
```

- [ ] **Step 5: Run focused graph/logger tests**

Run:

```bash
gofmt -w internal/graphclient/client_test.go pkg/logger/logger.go pkg/logger/logger_test.go
go test ./internal/graphclient ./pkg/logger -v
```

Expected: PASS.

- [ ] **Step 6: Commit Graph/logger guards**

Run:

```bash
git add internal/graphclient/client_test.go pkg/logger/logger.go pkg/logger/logger_test.go
git commit -m "test: strengthen password leak guards"
```

---

## Task 5: Final Documentation, Roadmap, and Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/README.md`
- Modify: `docs/superpowers/plans/roadmap.md`
- Move: `docs/superpowers/plans/active/2026-07-03-slice-07-password-data-protection.md`

- [ ] **Step 1: Update README worker behavior and data protection notes**

In `README.md`, in the `Worker Behavior` section, replace:

```markdown
The production app starts the HTTP server and password sync worker together. The hook encrypts accepted password payloads before enqueueing. The worker receives encrypted Service Bus messages, decrypts the password per processing attempt, calls Microsoft Graph, and zeroes plaintext buffers after use.
```

with:

```markdown
The production app starts the HTTP server and password sync worker together. The hook reads password JSON into mutable buffers, encrypts accepted password payloads before enqueueing, and zeroes plaintext buffers before returning. The worker receives encrypted Service Bus messages, decrypts the password per processing attempt, calls Microsoft Graph with borrowed plaintext bytes, and zeroes plaintext/message buffers before retry, DLQ, or settlement.
```

Add this sentence after the safe DLQ paragraph:

```markdown
Structured logging masks password, password-derived, secret, and token fields by key before records are emitted.
```

- [ ] **Step 2: Run targeted tests**

Run:

```bash
go test ./internal/migration ./internal/handler ./internal/worker ./internal/graphclient ./pkg/logger -v
```

Expected: PASS.

- [ ] **Step 3: Run full verification**

Run:

```bash
go test ./...
go vet ./...
```

Expected: both commands PASS.

- [ ] **Step 4: Run leak-focused repository scans**

Run:

```bash
rg -n 'Password:[[:space:]]*"|Password:[[:space:]]*string\(|Password[[:space:]]+string|json:"password"' internal pkg
rg -n 'slog\.(String|Any)\(".*password|slog\.(String|Any)\(".*secret|slog\.(String|Any)\(".*token' internal pkg cmd
rg -n 'DeadLetterEntry\{[^}]*Password|ApplicationProperties:.*password|ApplicationProperties:.*ciphertext|ApplicationProperties:.*nonce' internal -U
```

Expected:

- First scan only reports the hook request JSON tag, intentional test fixtures, or `Password string json:"-"` compatibility if still present; no runtime assignment should create plaintext strings.
- Second scan has no unmasked direct password/secret/token logging outside tests; if any runtime logging is needed, it must use the masking handler path.
- Third scan reports no safe DLQ or Service Bus application property persistence of plaintext password, ciphertext, nonce, or password-derived material.

- [ ] **Step 5: Mark the plan completed**

Move this plan and update its status header:

```bash
git mv docs/superpowers/plans/active/2026-07-03-slice-07-password-data-protection.md docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md
```

In `docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md`, change:

```markdown
> **Plan Status:** Active
```

to:

```markdown
> **Plan Status:** Completed
```

In `docs/superpowers/plans/README.md`, replace the current active plan line with:

```markdown
Current active detailed plan: not created. Next slice is Slice 8 Observability.
```

In `docs/superpowers/plans/roadmap.md`, replace the active plan line with:

```markdown
Current active detailed plan: not created. Next slice is Slice 8 Observability.
```

In the completion table, replace the Slice 7 row with:

```markdown
| 7. Password Data Protection | Done | `completed/2026-07-03-slice-07-password-data-protection.md` | Producer plaintext decoded into mutable buffers and zeroed on all paths; worker and Graph buffers covered by cleanup tests; log masking guards password/secret/token variants; verified with focused tests, full `go test ./...`, `go vet ./...`, and leak-focused `rg` scans |
```

- [ ] **Step 6: Commit final docs and plan completion**

Run:

```bash
git add README.md docs/superpowers/plans/README.md docs/superpowers/plans/roadmap.md docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md
git commit -m "docs: complete password data protection slice"
```

---

## Implementation Notes

- Do not convert plaintext password bytes to `string` in runtime code. Tests may use `string(passwordBytes)` only for assertions before buffers are expected to be zeroed.
- Do not add broad `recover` or silent fallback behavior for JSON decoding. Invalid password JSON must return the existing validation problem response.
- Do not add password ciphertext, nonce, or key id to Service Bus application properties or safe DLQ application properties.
- Do not change Graph request payload semantics; this slice only strengthens lifetime tests around existing request bodies.
- Keep `migration.PasswordSyncMessage.Password string json:"-"` only if existing compatibility tests still need it. Do not assign plaintext into that field in runtime code.
