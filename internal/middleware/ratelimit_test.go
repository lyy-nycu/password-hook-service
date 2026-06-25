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

func TestRateLimiterRejectsRequestAboveThreshold(t *testing.T) {
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
