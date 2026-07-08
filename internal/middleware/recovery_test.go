package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nycu/password-hook-service/internal/observability"
)

func TestRecoveryWritesProblemDetailsWithConfiguredBaseURL(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RecoveryWithProblemBase(log, "https://example.edu/problems")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"type":"https://example.edu/problems/internal-error"`)) {
		t.Fatalf("problem body = %s", rec.Body.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte(observability.ActionMiddlewareRecovered)) {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestRecoveryRecordsPanicOutcome(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	handler := RecoveryWithOptions(RecoveryOptions{
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
		ProblemBase: "https://example.edu/problems",
		Recorder:    recorder,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["middleware"] != "recovery" || samples[0].Labels["status"] != "500" || samples[0].Labels["outcome"] != "panic_recovered" {
		t.Fatalf("samples = %#v, want recovery panic metric", samples)
	}
}

func TestRecoveryWithOptionsDefaultsLoggerAndDoesNotLogPanicValue(t *testing.T) {
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	handler := RecoveryWithOptions(RecoveryOptions{
		ProblemBase: "https://example.edu/problems",
		Recorder:    recorder,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("cleartext-password")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	gotLogs := logs.String()
	if !bytes.Contains(logs.Bytes(), []byte(observability.ActionMiddlewareRecovered)) {
		t.Fatalf("logs = %s, want recovered action", gotLogs)
	}
	if bytes.Contains(logs.Bytes(), []byte("cleartext-password")) {
		t.Fatalf("logs leaked panic value: %s", gotLogs)
	}
}
