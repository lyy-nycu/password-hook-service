package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/requestid"
)

func TestHMACAllowsValidSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	signature := sign(secret, timestamp, nonce, body)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})

	middleware, err := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}
	rec := httptest.NewRecorder()
	req := signedRequest(body, timestamp, nonce, signature)

	middleware.Wrap(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func TestHMACZerosBodyAfterNextHandler(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001","password":"cleartext-password"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "11223344556677889900aabbccddeeff"
	middleware, err := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	var forwardedBody io.ReadCloser
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedBody = r.Body
		w.WriteHeader(http.StatusAccepted)
	})

	rec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(rec, signedRequest(body, timestamp, nonce, sign(secret, timestamp, nonce, body)))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if forwardedBody == nil {
		t.Fatal("next handler did not receive body")
	}
	forwarded, err := io.ReadAll(forwardedBody)
	if err != nil {
		t.Fatalf("read forwarded body: %v", err)
	}
	assertZeroedBytes(t, forwarded, "hmac forwarded body after handler")
}

func TestHMACZerosBodyWhenReadFails(t *testing.T) {
	t.Parallel()

	requestBody := &readErrorBody{data: []byte(`{"cn":"311551001","password":"cleartext-password"}`)}
	middleware, err := NewHMAC("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.Body = requestBody

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if len(requestBody.observed) == 0 {
		t.Fatal("read error body did not expose bytes to middleware")
	}
	assertZeroedBytes(t, requestBody.observed, "hmac request body after read error")
}

func TestHMACRecordsUnauthorizedRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		ProblemBase: "https://nycu.edu.tw/problems",
		Logger:      slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder:    recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", strings.NewReader(`{"password":"cleartext-password"}`))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 {
		t.Fatalf("middleware samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["middleware"] != "hmac" || labels["status"] != "401" || labels["outcome"] != "unauthorized" || labels["reason"] != "missing_or_invalid_signature_headers" {
		t.Fatalf("labels = %#v, want hmac unauthorized labels", labels)
	}
	gotLogs := logs.String()
	for _, want := range []string{observability.ActionMiddlewareRejected, `"middleware":"hmac"`, `"traceId":"trace-123"`, `"status":401`} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs = %s, want %s", gotLogs, want)
		}
	}
	if strings.Contains(gotLogs, "cleartext-password") || strings.Contains(gotLogs, "shared-secret") || strings.Contains(gotLogs, "sha256=") {
		t.Fatalf("logs leaked sensitive data: %s", gotLogs)
	}
}

func TestHMACRecordsReadBodyFailureRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	requestBody := &readErrorBody{data: []byte(`{"cn":"311551001","password":"cleartext-password"}`)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.Body = requestBody

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["reason"] != "failed_to_read_request_body" {
		t.Fatalf("samples = %#v, want failed_to_read_request_body reason", samples)
	}
	if !strings.Contains(logs.String(), `"reason":"failed_to_read_request_body"`) {
		t.Fatalf("logs = %s, want failed_to_read_request_body reason", logs.String())
	}
}

func TestHMACRecordsStaleTimestampRejection(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Add(-time.Minute).Unix()
	nonce := "abcdef0123456789abcdef0123456789"
	signature := sign(secret, timestamp, nonce, body)
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, signature))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["reason"] != "timestamp_outside_allowed_skew" {
		t.Fatalf("samples = %#v, want timestamp_outside_allowed_skew reason", samples)
	}
	if !strings.Contains(logs.String(), `"reason":"timestamp_outside_allowed_skew"`) {
		t.Fatalf("logs = %s, want timestamp_outside_allowed_skew reason", logs.String())
	}
}

func TestHMACRecordsSignatureMismatchRejection(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "fedcba9876543210fedcba9876543210"
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, sign("wrong-secret", timestamp, nonce, body)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["reason"] != "signature_mismatch" {
		t.Fatalf("samples = %#v, want signature_mismatch reason", samples)
	}
	if !strings.Contains(logs.String(), `"reason":"signature_mismatch"`) {
		t.Fatalf("logs = %s, want signature_mismatch reason", logs.String())
	}
}

func TestHMACRecordsNonceReplayRejection(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	signature := sign(secret, timestamp, nonce, body)
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	firstRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(firstRec, signedRequest(body, timestamp, nonce, signature))
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusAccepted)
	}

	secondRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(secondRec, signedRequest(body, timestamp, nonce, signature))
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("second status = %d, want %d", secondRec.Code, http.StatusUnauthorized)
	}

	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["reason"] != "nonce_replay" {
		t.Fatalf("samples = %#v, want nonce_replay reason", samples)
	}
	if !strings.Contains(logs.String(), `"reason":"nonce_replay"`) {
		t.Fatalf("logs = %s, want nonce_replay reason", logs.String())
	}
}

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

func TestHMACRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Add(-time.Minute).Unix()
	nonce := "abcdef0123456789abcdef0123456789"
	signature := sign(secret, timestamp, nonce, body)
	middleware, err := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, signature))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHMACRejectsBadSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "fedcba9876543210fedcba9876543210"
	middleware, err := NewHMAC("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, sign("wrong-secret", timestamp, nonce, body)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHMACDoesNotConsumeNonceForBadSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "00112233445566778899aabbccddeeff"
	store := NewMemoryNonceStore(60 * time.Second)
	middleware, err := NewHMAC("shared-secret", store, 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	bad := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(bad, signedRequest(body, timestamp, nonce, sign("wrong-secret", timestamp, nonce, body)))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want %d", bad.Code, http.StatusUnauthorized)
	}

	good := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(good, signedRequest(body, timestamp, nonce, sign("shared-secret", timestamp, nonce, body)))
	if good.Code != http.StatusAccepted {
		t.Fatalf("valid retry status = %d, want %d", good.Code, http.StatusAccepted)
	}
}

func TestHMACUsesConfiguredProblemBaseURL(t *testing.T) {
	t.Parallel()

	middleware, err := NewHMACWithProblemBase("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second, "https://example.edu/problems")
	if err != nil {
		t.Fatalf("NewHMACWithProblemBase returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, req)

	if !bytes.Contains(rec.Body.Bytes(), []byte(`"type":"https://example.edu/problems/unauthorized"`)) {
		t.Fatalf("problem body = %s", rec.Body.String())
	}
}

func TestNewHMACRejectsEmptySecret(t *testing.T) {
	t.Parallel()

	_, err := NewHMAC("", NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err == nil {
		t.Fatal("NewHMAC returned nil error for empty secret")
	}
}

func signedRequest(body []byte, timestamp int64, nonce string, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+signature)
	return req
}

func sign(secret string, timestamp int64, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

type readErrorBody struct {
	data     []byte
	observed []byte
}

func (b *readErrorBody) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	// Keep the read buffer alias so the test can verify middleware zeroing.
	b.observed = p[:n]
	return n, errors.New("read failed")
}

func (b *readErrorBody) Close() error { return nil }

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}
