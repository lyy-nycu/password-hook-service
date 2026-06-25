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
