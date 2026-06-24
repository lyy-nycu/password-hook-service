package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nycu/password-hook-service/internal/buildinfo"
)

func TestHealthzRoute(t *testing.T) {
	t.Parallel()

	mux := NewMux(Routes{
		Hook: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	}, buildinfo.Info{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q, want health json", rec.Body.String())
	}
}
