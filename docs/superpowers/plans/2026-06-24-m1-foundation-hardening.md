# M1 Foundation Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the M1 foundation slice so the password hook service has production-shaped HTTP behavior, validation, request tracing, HMAC authentication, masking logs, and clean configuration before adding Azure Service Bus or Microsoft Graph.

**Architecture:** Keep the service stdlib-first and preserve the existing package boundaries. Strengthen the current HTTP path from request entry through middleware, handler validation, migration policy, and response writing while leaving queue and Graph implementations behind interfaces for later slices.

**Tech Stack:** Go 1.26, `net/http`, `log/slog`, RFC 9457 problem responses, Dockerized Go toolchain.

---

## File Structure

- Modify: `internal/config/config.go` - add explicit validation and runtime mode handling.
- Create: `internal/config/config_test.go` - test required secrets and default values.
- Modify: `internal/app/app.go` - fail fast when config is invalid; wire request ID, recovery, access log, rate-limit, and HMAC in a stable order.
- Modify: `cmd/server/main.go` - handle config/app construction errors before serving.
- Modify: `internal/httpserver/server.go` - improve method handling, JSON helpers, and route-level consistency.
- Modify: `internal/httpserver/server_test.go` - test `/version`, method not allowed, and route behavior.
- Modify: `internal/handler/hook.go` - return correct status by error class and include trace IDs in every problem response.
- Modify: `internal/handler/hook_test.go` - cover accepted internal enqueue, skipped external email, validation errors, and enqueue failure.
- Modify: `internal/middleware/hmac.go` - inject clock, use configured nonce TTL, reject empty secret at construction, keep body reusable.
- Modify: `internal/middleware/hmac_test.go` - cover valid signature, bad signature, stale timestamp, replayed nonce, missing secret behavior.
- Modify: `internal/middleware/ratelimit.go` - implement source allowlist and anomalous per-IP limiter.
- Create: `internal/middleware/ratelimit_test.go` - test allowlist 401 behavior and 429 after threshold.
- Modify: `internal/middleware/accesslog.go` - use request ID, status capture, and masked attrs.
- Create: `internal/middleware/accesslog_test.go` - test status and request ID fields without logging passwords.
- Modify: `internal/middleware/recovery.go` - include request ID in RFC 9457 500 response.
- Create: `internal/middleware/recovery_test.go` - test panic becomes problem details.
- Modify: `internal/requestid/requestid.go` - add HTTP middleware and header propagation.
- Create: `internal/requestid/requestid_test.go` - test generated/request-provided request IDs.
- Modify: `pkg/logger/logger.go` - add a slog handler wrapper that masks attrs automatically.
- Modify: `pkg/logger/logger_test.go` - test nested/group attrs and handler-level masking.
- Modify: `pkg/problem/problem.go` - add helpers for common 400/401/429/500 responses.
- Modify: `pkg/problem/problem_test.go` - test content type, trace ID, and helper output.
- Modify: `README.md` - document M1 local run, HMAC signing example, and required env vars.

---

### Task 1: Config Validation

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `cmd/server/main.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestValidateRequiresHMACSecret(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTPAddr:           ":8080",
		EntraPrimaryDomain: "nycu.edu.tw",
		ProblemBaseURL:     "https://nycu.edu.tw/problems",
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without HMAC secret")
	}
}

func TestValidateAcceptsCompleteConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTPAddr:           ":8080",
		HMACSecret:         "shared-secret",
		EntraPrimaryDomain: "nycu.edu.tw",
		ProblemBaseURL:     "https://nycu.edu.tw/problems",
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/config
```

Expected: FAIL with `cfg.Validate undefined`.

- [ ] **Step 3: Implement config validation**

Add `Validate() error` to `internal/config/config.go`. Validate:
- `HTTPAddr` is not empty.
- `HOOK_HMAC_SECRET` is not empty.
- `ENTRA_PRIMARY_DOMAIN` is not empty and does not contain `@`.
- `PROBLEM_BASE_URL` starts with `https://`.
- `HMACClockSkew` and `NonceTTL` are positive.

Change `app.New(cfg config.Config)` to `app.New(cfg config.Config) (*App, error)` and call `cfg.Validate()` before wiring dependencies. Change `cmd/server/main.go` to exit with a logged error if `app.New` returns an error.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/config ./internal/app ./cmd/server
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/app/app.go cmd/server/main.go
git commit -m "feat: validate foundation configuration"
```

---

### Task 2: Request ID Middleware

**Files:**
- Modify: `internal/requestid/requestid.go`
- Create: `internal/requestid/requestid_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing tests**

Create `internal/requestid/requestid_test.go`:

```go
package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareUsesIncomingRequestID(t *testing.T) {
	t.Parallel()

	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = From(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "trace-123")
	rec := httptest.NewRecorder()

	Middleware(next).ServeHTTP(rec, req)

	if got != "trace-123" {
		t.Fatalf("request id from context = %q, want trace-123", got)
	}
	if rec.Header().Get("X-Request-ID") != "trace-123" {
		t.Fatalf("response X-Request-ID = %q, want trace-123", rec.Header().Get("X-Request-ID"))
	}
}

func TestMiddlewareGeneratesMissingRequestID(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if From(r.Context()) == "" {
			t.Fatal("request id was empty")
		}
	})

	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("response X-Request-ID was empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/requestid
```

Expected: FAIL with `undefined: Middleware`.

- [ ] **Step 3: Implement middleware**

Add `Middleware(next http.Handler) http.Handler` to `internal/requestid/requestid.go`. It should:
- Read `X-Request-ID`.
- Generate one with `New()` if missing.
- Store it in context using `With`.
- Write `X-Request-ID` response header.

Wire it as the outermost middleware in `internal/app/app.go`.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/requestid ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/requestid/requestid.go internal/requestid/requestid_test.go internal/app/app.go
git commit -m "feat: add request id middleware"
```

---

### Task 3: Problem Response Consistency

**Files:**
- Modify: `pkg/problem/problem.go`
- Modify: `pkg/problem/problem_test.go`
- Modify: `internal/handler/hook.go`
- Modify: `internal/middleware/hmac.go`
- Modify: `internal/middleware/recovery.go`

- [ ] **Step 1: Write failing tests**

Extend `pkg/problem/problem_test.go`:

```go
func TestUnauthorizedHelperIncludesTraceID(t *testing.T) {
	t.Parallel()

	p := Unauthorized("https://nycu.edu.tw/problems", "/api/v1/hook/password", "trace-123", "signature mismatch")

	if p.Type != "https://nycu.edu.tw/problems/unauthorized" {
		t.Fatalf("type = %q", p.Type)
	}
	if p.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d", p.Status)
	}
	if p.TraceID != "trace-123" {
		t.Fatalf("traceId = %q", p.TraceID)
	}
}

func TestValidationHelper(t *testing.T) {
	t.Parallel()

	p := Validation("https://nycu.edu.tw/problems", "/api/v1/hook/password", "trace-123", "Field 'cn' is required")

	if p.Title != "Validation Error" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Status != http.StatusBadRequest {
		t.Fatalf("status = %d", p.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./pkg/problem
```

Expected: FAIL with `undefined: Unauthorized` and `undefined: Validation`.

- [ ] **Step 3: Implement common helpers**

Add helpers to `pkg/problem/problem.go`:
- `Validation(baseURL, instance, traceID, detail string) Problem`
- `Unauthorized(baseURL, instance, traceID, detail string) Problem`
- `TooManyRequests(baseURL, instance, traceID, detail string) Problem`
- `Internal(baseURL, instance, traceID, detail string) Problem`

Update handler and middleware code to use these helpers and pass `requestid.From(r.Context())`.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./pkg/problem ./internal/handler ./internal/middleware
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/problem/problem.go pkg/problem/problem_test.go internal/handler/hook.go internal/middleware/hmac.go internal/middleware/recovery.go
git commit -m "feat: standardize problem responses"
```

---

### Task 4: HMAC Middleware Hardening

**Files:**
- Modify: `internal/middleware/hmac.go`
- Modify: `internal/middleware/hmac_test.go`

- [ ] **Step 1: Write failing tests**

Extend `internal/middleware/hmac_test.go` with tests for:
- Stale timestamp returns `401`.
- Reusing the same nonce returns `401`.
- Bad signature returns `401`.
- Empty secret construction returns an error.

Use this test shape:

```go
func TestHMACRejectsReplayedNonce(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	signature := sign(secret, timestamp, nonce, body)
	store := NewMemoryNonceStore(60 * time.Second)
	middleware, err := NewHMAC(secret, store, 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	first := signedRequest(body, timestamp, nonce, signature)
	firstRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusAccepted)
	}

	second := signedRequest(body, timestamp, nonce, signature)
	secondRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("second status = %d, want %d", secondRec.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/middleware
```

Expected: FAIL because `NewHMAC` currently does not return `(*HMAC, error)` and does not use the configured nonce TTL.

- [ ] **Step 3: Implement HMAC changes**

Change `NewHMAC` signature to:

```go
func NewHMAC(secret string, nonces NonceStore, skew time.Duration) (*HMAC, error)
```

Store `nonceTTL` on `HMAC` or on the nonce store so `Wrap` uses the configured TTL instead of hard-coded `60*time.Second`. Reject empty secret with an error. Update `internal/app/app.go` wiring to handle the constructor error.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/middleware ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/hmac.go internal/middleware/hmac_test.go internal/app/app.go
git commit -m "feat: harden hmac request validation"
```

---

### Task 5: Hook Handler Error Semantics

**Files:**
- Modify: `internal/handler/hook.go`
- Modify: `internal/handler/hook_test.go`
- Modify: `internal/migration/service.go`

- [ ] **Step 1: Write failing tests**

Extend `internal/handler/hook_test.go`:
- Internal student ID request returns `202` and enqueues exactly one message.
- Unknown CN format returns `400`, not `500`.
- Queue failure returns `500`.
- Validation failure returns RFC 9457 with trace ID.

Use this unknown-CN test:

```go
func TestHookRejectsUnknownCNAsBadRequest(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue)
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"bad cn!","password":"secret","displayName":"Bad","mail":"bad@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/handler
```

Expected: FAIL because unknown identity currently maps to a generic `500`.

- [ ] **Step 3: Implement error mapping**

In `internal/handler/hook.go`, use `errors.Is(err, migration.ErrUnknownIdentity)` and `errors.Is(err, migration.ErrExternalIdentity)` to return validation-style `400` only for invalid client input. Keep queue failures as `500`.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/handler ./internal/migration
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/hook.go internal/handler/hook_test.go internal/migration/service.go
git commit -m "feat: tighten hook handler error semantics"
```

---

### Task 6: Rate Limit and Source Allowlist

**Files:**
- Modify: `internal/middleware/ratelimit.go`
- Create: `internal/middleware/ratelimit_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing tests**

Create `internal/middleware/ratelimit_test.go`:

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterRejectsNonAllowlistedIP(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   500,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "198.51.100.10:12345"

	limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/middleware
```

Expected: FAIL because `RateLimitConfig` does not exist and `NewRateLimiter` has no config.

- [ ] **Step 3: Implement rate limiter**

Implement:
- CIDR allowlist from config; if list is non-empty and remote IP is outside the list, return `401`.
- Per-IP count with fixed one-second window; return `429` when count exceeds configured threshold.
- `X-Forwarded-For` support only when the immediate remote address is trusted by config; otherwise use `RemoteAddr`.

Add config fields:
- `PortalAllowedCIDRs []string`
- `RateLimitPerIP int`
- `RateLimitWindow time.Duration`

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/middleware ./internal/config ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/ratelimit.go internal/middleware/ratelimit_test.go internal/config/config.go internal/app/app.go
git commit -m "feat: add source allowlist and anomaly limit"
```

---

### Task 7: Structured Logging and Masking Handler

**Files:**
- Modify: `pkg/logger/logger.go`
- Modify: `pkg/logger/logger_test.go`
- Modify: `internal/middleware/accesslog.go`
- Create: `internal/middleware/accesslog_test.go`

- [ ] **Step 1: Write failing tests**

Extend `pkg/logger/logger_test.go`:

```go
func TestMaskingHandlerMasksSensitiveAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	handler := NewMaskingHandler(slog.NewJSONHandler(&buf, nil))
	log := slog.New(handler)

	log.Info("event", slog.String("password", "cleartext"), slog.String("cn", "311551001"))

	output := buf.String()
	if strings.Contains(output, "cleartext") {
		t.Fatalf("log output leaked password: %s", output)
	}
	if !strings.Contains(output, `"password":"****"`) {
		t.Fatalf("log output did not include masked password: %s", output)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./pkg/logger
```

Expected: FAIL with `undefined: NewMaskingHandler`.

- [ ] **Step 3: Implement masking handler**

Add a `slog.Handler` wrapper that masks attrs in `Handle`, `WithAttrs`, and nested groups. Update access log middleware to include:
- `traceId`
- `method`
- `path`
- `status`
- `durationMs`
- no request body fields

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./pkg/logger ./internal/middleware
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/logger/logger.go pkg/logger/logger_test.go internal/middleware/accesslog.go internal/middleware/accesslog_test.go
git commit -m "feat: enforce structured log masking"
```

---

### Task 8: HTTP Route and Version Behavior

**Files:**
- Modify: `internal/httpserver/server.go`
- Modify: `internal/httpserver/server_test.go`
- Modify: `internal/buildinfo/buildinfo.go`

- [ ] **Step 1: Write failing tests**

Extend `internal/httpserver/server_test.go`:
- `GET /version` returns configured version, commit, and build time.
- Unsupported method on `/healthz` returns `405`.
- Unknown route returns `404`.

Use this version test:

```go
func TestVersionRoute(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{Version: "1.2.3", Commit: "abc123", BuildTime: "2026-06-24T00:00:00Z"}
	mux := NewMux(Routes{Hook: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}, info)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"version":"1.2.3"`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/httpserver
```

Expected: FAIL if route behavior or imports are incomplete.

- [ ] **Step 3: Implement route behavior**

Keep `http.ServeMux` method patterns and add tests around existing behavior. If unsupported methods currently produce `405`, preserve it. Add JSON helper only if it removes duplicated header/encoder code.

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/httpserver ./internal/buildinfo
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/server.go internal/httpserver/server_test.go internal/buildinfo/buildinfo.go
git commit -m "test: cover foundation http routes"
```

---

### Task 9: README and Local HMAC Example

**Files:**
- Modify: `README.md`
- Create: `docs/examples/sign-hook-request.php`

- [ ] **Step 1: Write docs update**

Update `README.md` with:
- Required env vars for running locally.
- Dockerized test command.
- `curl` example for `/healthz`.
- HMAC signing instructions matching the PHP portal integration section.

Create `docs/examples/sign-hook-request.php` with:

```php
<?php

$payload = json_encode([
    'cn' => '311551001',
    'password' => 'cleartext_password',
    'displayName' => 'Test User',
    'mail' => 'test@nycu.edu.tw',
]);

$timestamp = time();
$nonce = bin2hex(random_bytes(16));
$secret = getenv('HOOK_HMAC_SECRET');
$signature = hash_hmac('sha256', $timestamp . '.' . $nonce . '.' . $payload, $secret);

echo "X-Hook-Timestamp: {$timestamp}\n";
echo "X-Hook-Nonce: {$nonce}\n";
echo "X-Hook-Signature: sha256={$signature}\n";
echo "{$payload}\n";
```

- [ ] **Step 2: Verify no docs placeholders**

Run:

```bash
rg "TBD|TODO|fill in|placeholder" README.md docs/examples/sign-hook-request.php
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/examples/sign-hook-request.php
git commit -m "docs: add m1 local usage guide"
```

---

### Task 10: Final M1 Verification

**Files:**
- Modify only files touched by earlier tasks if verification reveals issues.

- [ ] **Step 1: Run full verification**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."
```

Expected: PASS with exit code 0.

- [ ] **Step 2: Verify M1 design coverage**

Check:
- `/healthz` tested.
- `/version` tested.
- HMAC validates timestamp, nonce, and signature.
- RFC 9457 responses are used by handler and middleware.
- `pkg/problem` has helper tests.
- `pkg/logger` masks sensitive attrs at handler level.
- Request ID exists in problem responses and access logs.
- Config rejects missing HMAC secret.
- Rate limiter covers source allowlist and anomalous request rate.

- [ ] **Step 3: Commit any verification fixes**

If fixes were needed:

```bash
git add .
git commit -m "chore: finish m1 foundation verification"
```

If no fixes were needed, do not create an empty commit.

---

## Self-Review

- Spec coverage: This plan covers M1 foundation from the design document: Go service bootstrap, health/version routes, HMAC middleware, RFC 9457 problem handling, logger masking, request tracing, rate-limit/source protection, and local usage docs.
- Deferred by design: Azure Service Bus, Microsoft Graph, Key Vault, DLQ processing, Terraform implementation, CI security scanners, and staging smoke tests remain outside this slice. Those belong to M2-M5 slices.
- Placeholder scan: This plan contains no `TBD`, `TODO`, or unspecified implementation steps. Every task has exact files, failing-test intent, commands, expected results, and commit messages.
- Type consistency: New function names are consistent across tasks: `Config.Validate`, `requestid.Middleware`, `problem.Validation`, `problem.Unauthorized`, `problem.TooManyRequests`, `problem.Internal`, `NewHMAC`, `RateLimitConfig`, and `NewMaskingHandler`.
