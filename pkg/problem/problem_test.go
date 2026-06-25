package problem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteProblemDetails(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	p := New(
		"https://nycu.edu.tw/problems/validation-error",
		"Validation Error",
		http.StatusBadRequest,
		"Field 'cn' is required",
		"/api/v1/hook/password",
		"trace-123",
	)

	Write(rec, p)

	if got := rec.Code; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", got)
	}

	var body Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("problem response is not valid json: %v", err)
	}
	if body.TraceID != "trace-123" {
		t.Fatalf("traceId = %q, want trace-123", body.TraceID)
	}
}

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

func TestTooManyRequestsHelper(t *testing.T) {
	t.Parallel()

	p := TooManyRequests("https://nycu.edu.tw/problems", "/api/v1/hook/password", "trace-123", "rate limit exceeded")

	if p.Type != "https://nycu.edu.tw/problems/too-many-requests" {
		t.Fatalf("type = %q", p.Type)
	}
	if p.Title != "Too Many Requests" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d", p.Status)
	}
}

func TestInternalHelper(t *testing.T) {
	t.Parallel()

	p := Internal("https://nycu.edu.tw/problems", "/api/v1/hook/password", "trace-123", "unexpected error")

	if p.Type != "https://nycu.edu.tw/problems/internal-error" {
		t.Fatalf("type = %q", p.Type)
	}
	if p.Title != "Internal Server Error" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d", p.Status)
	}
}

func TestTypeURLDefaultsAndTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	if got := typeURL("", "validation-error"); got != DefaultBaseURL+"/validation-error" {
		t.Fatalf("typeURL with empty baseURL = %q", got)
	}
	if got := typeURL("https://nycu.edu.tw/problems/", "validation-error"); got != "https://nycu.edu.tw/problems/validation-error" {
		t.Fatalf("typeURL with trailing slash = %q", got)
	}
}
