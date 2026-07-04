# Slice 9 API Protection Draft Implementation Plan

> **Status:** Draft. This plan is a future-slice planning artifact only. Do not execute it until Slice 7 is merged, Slice 8 decisions are refreshed, this draft is refreshed against `main`, and the plan is promoted to `docs/superpowers/plans/active/`.
>
> **For agentic workers:** REQUIRED SUB-SKILL WHEN PROMOTED: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the hook API ingress behavior so production traffic must come from configured portal source ranges, anomalous request rates return `429`, non-allowed sources return `401`, and HMAC validation remains bounded, replay-safe, and documented.

**Architecture:** Tighten the existing middleware stack instead of adding a new ingress subsystem. Keep API protection in application code to cover local, test, and container runtime behavior; leave WAF, Azure Front Door, Terraform, and Azure DDoS controls to later infrastructure slices.

**Tech Stack:** Go `net/http`, existing `internal/middleware` HMAC and rate-limit packages, existing `internal/config` env loading and validation, RFC 9457 helpers in `pkg/problem`, unit and app-level integration tests.

---

## Draft Constraints

- Draft only. Do not implement this plan until it is refreshed and promoted to `active/`.
- Do not update `docs/superpowers/plans/README.md`, `docs/superpowers/plans/roadmap.md`, or any active plan pointer while this remains a draft.
- Refresh this plan after Slice 8 if Slice 8 changes middleware constructors, observability hooks, logger wiring, request IDs, or README structure.
- Keep Slice 9 focused on application-level API protection: source allowlist, anomalous traffic/rate protection, HMAC/middleware behavior, tests, and docs.
- Do not add Terraform, Azure Front Door, WAF, managed DDoS, Container Apps ingress policy, private endpoint, VPN, or DNS changes in this slice.
- Do not add Redis or distributed rate-limit infrastructure in this slice. The in-process limiter remains the application fallback; infrastructure-wide throttling belongs to later infrastructure slices.
- Do not log request bodies, passwords, HMAC signatures, HMAC secrets, nonces, ciphertext, or authorization headers.

## Current Context

- Current portal topology is `nginx simple load balancer -> two portal web servers -> password hook service`. The login API runs on the two portal web servers, so hook API calls are expected to originate from the two portal web-server egress IPs, currently described as `<portal-egress-ip-1>` and `<portal-egress-ip-2>`.
- `internal/middleware/ratelimit.go` already combines source allowlist and fixed-window per-IP rate limiting.
- `internal/config/config.go` currently loads `PORTAL_ALLOWED_CIDRS` and `RATE_LIMIT_PER_IP`, but the allowlist is optional and `RateLimitWindow` is fixed at one second.
- `internal/app/app.go` already wires middleware in the useful runtime order: request ID, access log, recovery, source/rate limiter, HMAC, hook.
- `internal/middleware/hmac.go` validates timestamp freshness, signature, and nonce uniqueness, and it only consumes a nonce after the signature is valid.
- `internal/middleware/hmac.go` currently reads the whole request body before verifying the signature. Slice 9 should bound this read because the portal payload is small.
- `README.md` currently describes API protection settings as optional local settings. Slice 9 should document them as required production settings and local development requirements.

## Rate-Limit Story

- `RATE_LIMIT_PER_IP` applies to each immediate portal web-server source IP. With two portal web servers, the expected aggregate cap is approximately `2 * RATE_LIMIT_PER_IP` when traffic is evenly balanced.
- `PORTAL_ALLOWED_CIDRS` should be configured to the two portal web-server egress IPs as `/32` entries, or to the smallest CIDR ranges that exclusively contain those servers.
- The application should not use end-user `X-Forwarded-For` as the rate-limit key in this slice. The API protection goal is to catch anomalous output from either portal web server, including portal retry loops, bugs, or uneven load balancer distribution.
- `RATE_LIMIT_PER_IP=500` is a default guardrail, not a production truth. Production sizing should use observed peak successful-login hook rate per portal web server, with enough headroom that normal login bursts do not receive `429`.
- The portal must treat `429` as password-sync hook rejection only. It must not fail the user login, and it must not immediately retry in a tight loop.

## HMAC Story

- The portal and password hook service share one HMAC secret for this API contract. The portal uses that secret to sign each hook request, and the hook service uses the same secret to verify authenticity and body integrity.
- The portal must send `X-Hook-Timestamp`, `X-Hook-Nonce`, and `X-Hook-Signature: sha256=<hmac>` on each password hook call.
- The shared secret must be kept out of code and static config files. Production should load it from the approved secret store; local development may use explicit environment configuration.
- HMAC protects the request from forged callers, body tampering, and replay within the nonce/timestamp window. Source allowlisting and rate limiting are still required because HMAC is not a traffic-volume control.

## Per-User Hook Story

- Each password hook request represents one successful-login password event for one LDAP identity. The request body carries one `cn`, one cleartext password, one display name, and one mail value.
- The hook can process a single end-user event: internal student/employee identities are enqueued for password sync, external email identities are skipped, and invalid identities are rejected according to existing handler behavior.
- Slice 9 does not add per-user rate limiting. The anomaly limiter keys by immediate portal web-server source IP so the service can detect abnormal output from either portal server before trusting request-body fields.
- Any future per-user rate limit must happen after HMAC succeeds because only then can the service trust the signed body fields such as `cn`. That is outside this slice unless explicitly requested.

## File Structure

- Modify `internal/config/config.go`: require `PORTAL_ALLOWED_CIDRS`, load `RATE_LIMIT_WINDOW`, add `HOOK_MAX_BODY_BYTES`, and validate positive API protection values.
- Modify `internal/config/config_test.go`: cover allowlist requirement, duration/body-size loading, and validation failures.
- Modify `internal/middleware/ratelimit.go`: make source/rate decisions explicit and testable while preserving `401` for disallowed source ranges and `429` for over-limit traffic.
- Modify `internal/middleware/ratelimit_test.go`: cover allowlisted source success, disallowed source `401`, per-source rate limiting, ignored forwarded client headers, and window reset.
- Modify `internal/middleware/hmac.go`: add options for max body bytes, return generic client-facing auth failures, and preserve nonce consumption behavior.
- Modify `internal/middleware/hmac_test.go`: cover generic auth failure detail, max body rejection, valid signature behavior, and nonce replay behavior.
- Modify `pkg/problem/problem.go`: add an RFC 9457 `413 Payload Too Large` helper for bounded HMAC body reads.
- Modify `pkg/problem/problem_test.go`: cover the new payload-too-large problem type.
- Modify `internal/app/app.go`: pass the API protection config into HMAC and rate-limit middleware.
- Modify `internal/app/app_test.go`: assert app-level allowlist, rate-limit-before-HMAC, and config requirements without Service Bus.
- Modify `README.md`: document required API protection environment variables, status codes, middleware behavior, and out-of-scope infrastructure controls.

---

### Task 1: Require Portal Allowlist And Load Protection Knobs

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing config tests**

Add these tests to `internal/config/config_test.go`:

```go
func TestValidateHTTPRequiresPortalAllowedCIDRs(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PortalAllowedCIDRs = nil

	if err := cfg.ValidateHTTP(); err == nil || err.Error() != "PORTAL_ALLOWED_CIDRS is required" {
		t.Fatalf("ValidateHTTP error = %v, want PORTAL_ALLOWED_CIDRS is required", err)
	}
}

func TestLoadAPIProtectionSettings(t *testing.T) {
	t.Setenv("PORTAL_ALLOWED_CIDRS", " 192.0.2.0/24, 2001:db8::/32 ")
	t.Setenv("RATE_LIMIT_PER_IP", "750")
	t.Setenv("RATE_LIMIT_WINDOW", "2s")
	t.Setenv("HOOK_MAX_BODY_BYTES", "32768")

	cfg := Load()

	if got, want := cfg.PortalAllowedCIDRs, []string{"192.0.2.0/24", "2001:db8::/32"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PortalAllowedCIDRs = %#v, want %#v", got, want)
	}
	if cfg.RateLimitPerIP != 750 {
		t.Fatalf("RateLimitPerIP = %d, want 750", cfg.RateLimitPerIP)
	}
	if cfg.RateLimitWindow != 2*time.Second {
		t.Fatalf("RateLimitWindow = %s, want 2s", cfg.RateLimitWindow)
	}
	if cfg.HookMaxBodyBytes != 32768 {
		t.Fatalf("HookMaxBodyBytes = %d, want 32768", cfg.HookMaxBodyBytes)
	}
}

func TestValidateHTTPRejectsInvalidAPIProtectionSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "zero rate limit", edit: func(cfg *Config) { cfg.RateLimitPerIP = 0 }, want: "RateLimitPerIP must be positive"},
		{name: "negative rate limit", edit: func(cfg *Config) { cfg.RateLimitPerIP = -1 }, want: "RateLimitPerIP must be positive"},
		{name: "zero rate window", edit: func(cfg *Config) { cfg.RateLimitWindow = 0 }, want: "RateLimitWindow must be positive"},
		{name: "negative rate window", edit: func(cfg *Config) { cfg.RateLimitWindow = -time.Second }, want: "RateLimitWindow must be positive"},
		{name: "zero max body", edit: func(cfg *Config) { cfg.HookMaxBodyBytes = 0 }, want: "HookMaxBodyBytes must be positive"},
		{name: "negative max body", edit: func(cfg *Config) { cfg.HookMaxBodyBytes = -1 }, want: "HookMaxBodyBytes must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := completeConfig()
			tt.edit(&cfg)

			err := cfg.ValidateHTTP()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateHTTP error = %v, want %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run config tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/config`

Expected: FAIL because `Config.HookMaxBodyBytes`, `RATE_LIMIT_WINDOW` loading, and required `PORTAL_ALLOWED_CIDRS` behavior do not exist yet.

- [ ] **Step 3: Add config fields and env loading**

Update `internal/config/config.go`:

```go
type Config struct {
	SecretsSource                 string
	KeyVaultURL                   string
	KeyVaultSecretNames           KeyVaultSecretNames
	HTTPAddr                      string
	HMACSecret                    string
	EntraPrimaryDomain            string
	EntraFallbackDomain           string
	ProblemBaseURL                string
	HMACClockSkew                 time.Duration
	NonceTTL                      time.Duration
	PortalAllowedCIDRs            []string
	RateLimitPerIP                int
	RateLimitWindow               time.Duration
	HookMaxBodyBytes              int64
	ServiceBusConnectionString    string
	ServiceBusQueueName           string
	ServiceBusDeadLetterQueueName string
	PasswordMessageTTL            time.Duration
	PasswordEncryptionKeyB64      string
	PasswordEncryptionKeyID       string
	GraphTenantID                 string
	GraphClientID                 string
	GraphClientSecret             string
}
```

Change the `Load()` API protection settings to:

```go
PortalAllowedCIDRs:            csvEnv("PORTAL_ALLOWED_CIDRS"),
RateLimitPerIP:                intEnv("RATE_LIMIT_PER_IP", 500),
RateLimitWindow:               durationEnv("RATE_LIMIT_WINDOW", time.Second),
HookMaxBodyBytes:              int64Env("HOOK_MAX_BODY_BYTES", 64*1024),
```

Add helpers:

```go
func int64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}
```

- [ ] **Step 4: Require positive API protection config**

Change the API protection portion of `ValidateHTTP()` in `internal/config/config.go`:

```go
case len(c.PortalAllowedCIDRs) == 0:
	return errors.New("PORTAL_ALLOWED_CIDRS is required")
case c.RateLimitPerIP <= 0:
	return errors.New("RateLimitPerIP must be positive")
case c.RateLimitWindow <= 0:
	return errors.New("RateLimitWindow must be positive")
case c.HookMaxBodyBytes <= 0:
	return errors.New("HookMaxBodyBytes must be positive")
default:
	return validateCIDRs(c.PortalAllowedCIDRs)
```

- [ ] **Step 5: Update test config helpers**

In `internal/config/config_test.go`, update `completeConfig()`:

```go
PortalAllowedCIDRs:            []string{"192.0.2.0/24"},
RateLimitPerIP:                500,
RateLimitWindow:               time.Second,
HookMaxBodyBytes:              64 * 1024,
```

In `internal/app/app_test.go`, update `completeAppConfig()` with the same API protection values:

```go
PortalAllowedCIDRs:            []string{"192.0.2.0/24"},
RateLimitPerIP:                500,
RateLimitWindow:               time.Second,
HookMaxBodyBytes:              64 * 1024,
```

- [ ] **Step 6: Run focused tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/config ./internal/app`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/app/app_test.go
git commit -m "feat: require api protection config"
```

### Task 2: Make Source Allowlist And Rate Decisions Explicit

**Files:**
- Modify: `internal/middleware/ratelimit.go`
- Modify: `internal/middleware/ratelimit_test.go`

- [ ] **Step 1: Add failing middleware behavior tests**

Replace and extend `internal/middleware/ratelimit_test.go` with tests covering these cases:

```go
func TestRateLimiterAllowsAllowlistedSource(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   500,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"

	limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

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

func TestRateLimiterLimitsImmediatePortalSource(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   1,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
	})
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusAccepted)
	}

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimiterIgnoresForwardedForWhenKeyingPortalSource(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   1,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
	})
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	for i, forwarded := range []string{"203.0.113.1", "203.0.113.2"} {
		want := []int{http.StatusAccepted, http.StatusTooManyRequests}[i]
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		req.Header.Set("X-Forwarded-For", forwarded)
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   1,
		Window:       time.Millisecond,
		ProblemBase:  "https://nycu.edu.tw/problems",
	})
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusAccepted)
	}

	time.Sleep(2 * time.Millisecond)

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusAccepted)
	}
}
```

- [ ] **Step 2: Run middleware tests and verify the new expectations**

Run: `/usr/local/go/bin/go test ./internal/middleware`

Expected: FAIL only if the existing implementation does not preserve each explicit behavior after the tests are added. If all tests already pass, continue to Step 3 to make the behavior clearer without changing results.

- [ ] **Step 3: Refactor source and rate-key selection into named helpers**

Update `internal/middleware/ratelimit.go` so `Wrap` reads as:

```go
func (l *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceIP := remoteIP(r)
		if !l.sourceAllowed(sourceIP) {
			problem.Write(w, problem.Unauthorized(l.problemBase, r.URL.Path, requestid.From(r.Context()), "source ip is not allowed"))
			return
		}

		key := l.rateKey(sourceIP)
		if !l.allow(key, time.Now()) {
			problem.Write(w, problem.TooManyRequests(l.problemBase, r.URL.Path, requestid.From(r.Context()), "request rate exceeded"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) sourceAllowed(sourceIP net.IP) bool {
	return len(l.allowedCIDRs) > 0 && containsIP(l.allowedCIDRs, sourceIP)
}

func (l *RateLimiter) rateKey(sourceIP net.IP) string {
	return sourceIP.String()
}
```

Remove `forwardedClientIP` if no other code uses it. Slice 9 keys the anomaly limiter by immediate portal web-server source IP so it catches retry loops and aggregate bursts from `<portal-egress-ip-1>` or `<portal-egress-ip-2>` instead of spreading the limit across end-user forwarded addresses.

- [ ] **Step 4: Run middleware tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/middleware`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/ratelimit.go internal/middleware/ratelimit_test.go
git commit -m "test: lock api source and rate limit behavior"
```

### Task 3: Bound HMAC Body Reads And Normalize Auth Failures

**Files:**
- Modify: `internal/middleware/hmac.go`
- Modify: `internal/middleware/hmac_test.go`
- Modify: `pkg/problem/problem.go`
- Modify: `pkg/problem/problem_test.go`

- [ ] **Step 1: Add failing problem helper test**

Add to `pkg/problem/problem_test.go`:

```go
func TestPayloadTooLargeProblem(t *testing.T) {
	t.Parallel()

	p := PayloadTooLarge("https://nycu.edu.tw/problems", "/api/v1/hook/password", "trace-123", "request body is too large")

	if p.Type != "https://nycu.edu.tw/problems/payload-too-large" {
		t.Fatalf("type = %q", p.Type)
	}
	if p.Title != "Payload Too Large" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", p.Status)
	}
	if p.TraceID != "trace-123" {
		t.Fatalf("traceId = %q", p.TraceID)
	}
}
```

- [ ] **Step 2: Add failing HMAC hardening tests**

Add these tests to `internal/middleware/hmac_test.go`:

```go
func TestHMACRejectsBodyAboveConfiguredLimit(t *testing.T) {
	t.Parallel()

	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), HMACOptions{
		Skew:         30 * time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		MaxBodyBytes: 8,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	req := signedRequest(body, timestamp, nonce, sign("shared-secret", timestamp, nonce, body))
	rec := httptest.NewRecorder()

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"type":"https://nycu.edu.tw/problems/payload-too-large"`)) {
		t.Fatalf("problem body = %s", rec.Body.String())
	}
}

func TestHMACUsesGenericClientDetailForAuthFailures(t *testing.T) {
	t.Parallel()

	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), HMACOptions{
		Skew:         30 * time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		MaxBodyBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "fedcba9876543210fedcba9876543210"
	req := signedRequest(body, timestamp, nonce, sign("wrong-secret", timestamp, nonce, body))
	rec := httptest.NewRecorder()

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"detail":"request authentication failed"`)) {
		t.Fatalf("problem body = %s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("signature mismatch")) {
		t.Fatalf("problem body exposed internal auth reason: %s", rec.Body.String())
	}
}

func TestNewHMACWithOptionsRejectsInvalidBodyLimit(t *testing.T) {
	t.Parallel()

	_, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), HMACOptions{
		Skew:         30 * time.Second,
		MaxBodyBytes: 0,
	})

	if err == nil || err.Error() != "hmac max body bytes must be positive" {
		t.Fatalf("NewHMACWithOptions error = %v, want hmac max body bytes must be positive", err)
	}
}
```

- [ ] **Step 3: Run focused tests and verify they fail**

Run: `/usr/local/go/bin/go test ./pkg/problem ./internal/middleware`

Expected: FAIL because `PayloadTooLarge`, `HMACOptions`, and max body enforcement do not exist.

- [ ] **Step 4: Add payload-too-large problem helper**

Add to `pkg/problem/problem.go`:

```go
func PayloadTooLarge(baseURL, instance, traceID, detail string) Problem {
	return New(typeURL(baseURL, "payload-too-large"), "Payload Too Large", http.StatusRequestEntityTooLarge, detail, instance, traceID)
}
```

- [ ] **Step 5: Add HMAC options and bounded body reads**

Update `internal/middleware/hmac.go`:

```go
type HMACOptions struct {
	Skew         time.Duration
	ProblemBase  string
	MaxBodyBytes int64
}

type HMAC struct {
	secret       []byte
	nonces       NonceStore
	skew         time.Duration
	nonceTTL     time.Duration
	problemBase  string
	maxBodyBytes int64
}

func NewHMAC(secret string, nonces NonceStore, skew time.Duration) (*HMAC, error) {
	return NewHMACWithOptions(secret, nonces, HMACOptions{
		Skew:         skew,
		ProblemBase:  problem.DefaultBaseURL,
		MaxBodyBytes: 64 * 1024,
	})
}

func NewHMACWithProblemBase(secret string, nonces NonceStore, skew time.Duration, problemBase string) (*HMAC, error) {
	return NewHMACWithOptions(secret, nonces, HMACOptions{
		Skew:         skew,
		ProblemBase:  problemBase,
		MaxBodyBytes: 64 * 1024,
	})
}

func NewHMACWithOptions(secret string, nonces NonceStore, opts HMACOptions) (*HMAC, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("hmac secret is required")
	}
	if opts.Skew <= 0 {
		return nil, errors.New("hmac clock skew must be positive")
	}
	if opts.MaxBodyBytes <= 0 {
		return nil, errors.New("hmac max body bytes must be positive")
	}
	return &HMAC{
		secret:       []byte(secret),
		nonces:       nonces,
		skew:         opts.Skew,
		nonceTTL:     nonceTTL(nonces),
		problemBase:  opts.ProblemBase,
		maxBodyBytes: opts.MaxBodyBytes,
	}, nil
}
```

Change the first body read in `Wrap`:

```go
body, err := io.ReadAll(io.LimitReader(r.Body, m.maxBodyBytes+1))
if err != nil {
	m.writeUnauthorized(w, r)
	return
}
if int64(len(body)) > m.maxBodyBytes {
	problem.Write(w, problem.PayloadTooLarge(m.problemBase, r.URL.Path, requestid.From(r.Context()), "request body is too large"))
	return
}
r.Body = io.NopCloser(bytes.NewReader(body))
```

Change all auth failure calls to use a generic client-facing detail:

```go
func (m HMAC) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, problem.Unauthorized(m.problemBase, r.URL.Path, requestid.From(r.Context()), "request authentication failed"))
}
```

Replace calls such as:

```go
m.writeUnauthorized(w, r, "signature mismatch")
```

with:

```go
m.writeUnauthorized(w, r)
```

- [ ] **Step 6: Run focused tests and verify they pass**

Run: `/usr/local/go/bin/go test ./pkg/problem ./internal/middleware`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/problem/problem.go pkg/problem/problem_test.go internal/middleware/hmac.go internal/middleware/hmac_test.go
git commit -m "feat: harden hmac request handling"
```

### Task 4: Wire API Protection Through App Construction

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Add failing app-level API protection tests**

Add these tests to `internal/app/app_test.go`:

```go
func TestNewWithQueueRequiresPortalAllowedCIDRs(t *testing.T) {
	t.Parallel()

	cfg := completeAppConfig()
	cfg.PortalAllowedCIDRs = nil

	application, err := NewWithQueue(cfg, &captureQueue{})

	if err == nil || err.Error() != "PORTAL_ALLOWED_CIDRS is required" {
		t.Fatalf("NewWithQueue error = %v, want PORTAL_ALLOWED_CIDRS is required", err)
	}
	if application != nil {
		t.Fatalf("NewWithQueue application = %#v, want nil", application)
	}
}

func TestAppRejectsNonAllowlistedSourceBeforeHMAC(t *testing.T) {
	logs, restore := captureDefaultLogger()
	defer restore()

	cfg := completeAppConfig()
	cfg.PortalAllowedCIDRs = []string{"192.0.2.0/24"}
	application, err := NewWithQueue(cfg, &captureQueue{})
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.10:12345"
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("source ip is not allowed")) {
		t.Fatalf("problem body = %s", rec.Body.String())
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("logs leaked password: %s", logs.String())
	}
}

func TestAppRateLimitsBeforeHMAC(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PortalAllowedCIDRs = []string{"192.0.2.0/24"}
	cfg.RateLimitPerIP = 1
	cfg.RateLimitWindow = time.Second
	application, err := NewWithQueue(cfg, &captureQueue{})
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.10:12345"
		rec := httptest.NewRecorder()
		application.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d: %s", i+1, rec.Code, want, rec.Body.String())
		}
	}
}

func TestAppRejectsOversizedHookBody(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PortalAllowedCIDRs = []string{"192.0.2.0/24"}
	cfg.HookMaxBodyBytes = 8
	application, err := NewWithQueue(cfg, &captureQueue{})
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.10:12345"
	signRequest(req, cfg.HMACSecret, body)
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run app tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: FAIL until `app.go` passes `HookMaxBodyBytes` into HMAC construction and all app test helpers use allowlisted remote addresses.

- [ ] **Step 3: Wire HMAC options in app construction**

Change the HMAC constructor call in `internal/app/app.go`:

```go
hmacMiddleware, err := middleware.NewHMACWithOptions(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), middleware.HMACOptions{
	Skew:         cfg.HMACClockSkew,
	ProblemBase:  cfg.ProblemBaseURL,
	MaxBodyBytes: cfg.HookMaxBodyBytes,
})
```

Keep the existing middleware wrapping order:

```go
hookHandler := hmacMiddleware.Wrap(hook)
hookHandler = rateLimiter.Wrap(hookHandler)
hookHandler = middleware.RecoveryWithProblemBase(slog.Default(), cfg.ProblemBaseURL)(hookHandler)
hookHandler = middleware.AccessLog(slog.Default())(hookHandler)
hookHandler = requestid.Middleware(hookHandler)
```

- [ ] **Step 4: Set allowlisted remote addresses in existing app tests**

For every signed hook request in `internal/app/app_test.go`, set an allowlisted `RemoteAddr` before `application.ServeHTTP`:

```go
req.RemoteAddr = "192.0.2.10:12345"
```

This includes the existing enqueue, ciphertext, external skip, and shared password codec tests.

- [ ] **Step 5: Run app tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire api protection settings"
```

### Task 5: Document API Protection Behavior

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update local environment examples**

In `README.md`, move API protection variables from optional local settings into the required local run block:

```bash
export PORTAL_ALLOWED_CIDRS="127.0.0.1/32,::1/128"
export RATE_LIMIT_PER_IP="500"
export RATE_LIMIT_WINDOW="1s"
export HOOK_MAX_BODY_BYTES="65536"
```

Keep them in the `docker run` `-e` list:

```bash
  -e PORTAL_ALLOWED_CIDRS \
  -e RATE_LIMIT_PER_IP \
  -e RATE_LIMIT_WINDOW \
  -e HOOK_MAX_BODY_BYTES \
```

- [ ] **Step 2: Add an API Protection section**

Add this section after "Local HMAC Request":

```markdown
## API Protection

`POST /api/v1/hook/password` is protected in application middleware before the hook handler runs:

- Requests from sources outside `PORTAL_ALLOWED_CIDRS` return `401 Unauthorized`.
- Requests above `RATE_LIMIT_PER_IP` during `RATE_LIMIT_WINDOW` return `429 Too Many Requests`.
- HMAC authentication failures return `401 Unauthorized` with a generic problem detail.
- Request bodies larger than `HOOK_MAX_BODY_BYTES` return `413 Payload Too Large`.

The portal and password hook service share the HMAC secret for this API. The portal signs each hook request with `X-Hook-Timestamp`, `X-Hook-Nonce`, and `X-Hook-Signature`; the hook service verifies the same secret before accepting the body as authentic. Keep this secret in the approved secret store for production and out of source code.

Each hook request represents one successful-login password event for one LDAP identity. The service can process a single end-user event, but Slice 9 does not add per-user rate limiting. Any future per-user limiter must run after HMAC succeeds because only then can the service trust signed body fields such as `cn`.

For the current portal topology, configure `PORTAL_ALLOWED_CIDRS` to the full `/32` CIDRs for the two portal web-server egress addresses (`41.155` and `41.177` as currently described). `RATE_LIMIT_PER_IP` is enforced per immediate portal web-server source IP. With two portal web servers, the expected aggregate cap is approximately `2 * RATE_LIMIT_PER_IP` when traffic is evenly balanced.

The application intentionally does not use `X-Forwarded-For` as the anomaly rate-limit key in this slice. The goal is to catch abnormal hook output from either portal web server, including retry loops, bugs, or uneven load balancer distribution.

Size `RATE_LIMIT_PER_IP` from observed peak successful-login hook rate per portal web server, with enough headroom that normal login bursts do not receive `429`. The default `500` is a guardrail, not a fixed production capacity decision. The portal must not fail user login on `429`, and it must not immediately retry in a tight loop.

Infrastructure protections such as Azure Front Door, WAF rules, Azure DDoS Protection, private endpoints, VPN routing, and Terraform ingress policy are outside this application slice and are handled by later infrastructure slices.
```

- [ ] **Step 3: Update the configuration table**

Change the API protection rows in the README configuration table:

```markdown
| `PORTAL_ALLOWED_CIDRS` | empty | Required; comma-separated source CIDR allowlist for portal web-server egress IPs |
| `RATE_LIMIT_PER_IP` | `500` | Required positive value; per-source-IP request threshold during `RATE_LIMIT_WINDOW`; with two portal web servers, aggregate capacity is approximately `2 * RATE_LIMIT_PER_IP` |
| `RATE_LIMIT_WINDOW` | `1s` | Required positive Go duration for the anomaly rate-limit window |
| `HOOK_MAX_BODY_BYTES` | `65536` | Required positive byte limit for signed hook request bodies |
```

- [ ] **Step 4: Run a docs sanity check**

Run: `rg -n "Optional local API protection|PORTAL_ALLOWED_CIDRS|RATE_LIMIT_WINDOW|HOOK_MAX_BODY_BYTES|Azure Front Door|WAF|DDoS" README.md`

Expected:
- No `Optional local API protection` heading remains.
- `PORTAL_ALLOWED_CIDRS`, `RATE_LIMIT_WINDOW`, and `HOOK_MAX_BODY_BYTES` appear in local run and configuration docs.
- Azure Front Door, WAF, and DDoS are mentioned only as infrastructure-slice exclusions.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document api protection settings"
```

### Task 6: Full Verification And Leak Scan

**Files:**
- Verify: all touched Go and docs files

- [ ] **Step 1: Format and run full verification**

Run:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Run password and secret leak scans**

Run:

```bash
rg -n "cleartext-password|hook-password|worker-password|local-development-secret|shared-secret" --glob '!docs/superpowers/plans/**'
rg -n "X-Hook-Signature|HOOK_HMAC_SECRET|PasswordCiphertext|PasswordNonce" internal README.md
```

Expected:
- The first scan only reports test fixture strings where they are intentionally used.
- The second scan shows configuration, request-signing, and encrypted payload field references, not logging of secret values or request bodies.

- [ ] **Step 3: Verify no infrastructure files changed**

Run:

```bash
git diff --name-only HEAD~5..HEAD
```

Expected changed paths are limited to:

```text
README.md
internal/app/app.go
internal/app/app_test.go
internal/config/config.go
internal/config/config_test.go
internal/middleware/hmac.go
internal/middleware/hmac_test.go
internal/middleware/ratelimit.go
internal/middleware/ratelimit_test.go
pkg/problem/problem.go
pkg/problem/problem_test.go
```

No files under `deploy/terraform/` are changed.

- [ ] **Step 4: Commit verification notes if the active-plan convention requires it**

If the promoted active plan tracks verification notes in the plan file, mark completed checkboxes and add a short verification note to the active copy only:

```markdown
Verification completed with `/usr/local/go/bin/go test ./...`, `/usr/local/go/bin/go vet ./...`, and leak-focused `rg` scans.
```

Do not edit this draft file during implementation unless the owner explicitly asks to refresh the draft.

---

## Self-Review

- Spec coverage: This draft covers the Slice 9 roadmap goal: source allowlist enforcement, anomalous traffic `429`, non-allowed source `401`, HMAC middleware hardening, tests, and documentation.
- Boundary check: This draft intentionally excludes WAF, Azure Front Door, Terraform, DDoS, private networking, Redis, and distributed rate limiting.
- Placeholder scan: No `TBD`, unresolved placeholders, or unspecified test areas remain.
- Type consistency: New names are consistent across tasks: `HookMaxBodyBytes`, `HOOK_MAX_BODY_BYTES`, `RATE_LIMIT_WINDOW`, `HMACOptions`, `NewHMACWithOptions`, and `problem.PayloadTooLarge`.
