package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/requestid"
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

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusAccepted)
	}

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.2")
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
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

func TestRateLimiterRecordsSourceAllowlistRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   500,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		Logger:       slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder:     recorder,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if labels["middleware"] != "ratelimit" || labels["status"] != "401" || labels["reason"] != "source_ip_not_allowed" {
		t.Fatalf("labels = %#v, want source allowlist rejection", labels)
	}
	if !strings.Contains(logs.String(), observability.ActionMiddlewareRejected) {
		t.Fatalf("logs = %s, want middleware rejection action", logs.String())
	}
}

func TestRateLimiterRecordsThresholdRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   1,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		Recorder:     recorder,
	})
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(second, req)

	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["status"] != "429" || samples[0].Labels["reason"] != "request_rate_exceeded" {
		t.Fatalf("samples = %#v, want rate-limit rejection", samples)
	}
}
