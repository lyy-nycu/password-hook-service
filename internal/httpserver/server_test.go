package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
	if !strings.Contains(rec.Body.String(), `"commit":"abc123"`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHealthzRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	mux := NewMux(Routes{Hook: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}, buildinfo.Info{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	t.Parallel()

	mux := NewMux(Routes{Hook: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}, buildinfo.Info{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestNewSetsReadHeaderTimeout(t *testing.T) {
	srv := New(":0", Routes{}, buildinfo.Info{})

	// server field is unexported, but this test lives in the same package
	// (httpserver), so srv.server is visible here without exporting
	// anything new. New must return a *Server whose underlying http.Server
	// has a non-zero ReadHeaderTimeout, which gosec's G112 check requires.
	if srv.server.ReadHeaderTimeout == 0 {
		t.Fatal("expected ReadHeaderTimeout to be set to a non-zero duration, got 0 (Slowloris risk, gosec G112)")
	}
}
