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
