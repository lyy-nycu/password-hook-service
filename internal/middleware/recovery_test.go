package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
	if !bytes.Contains(logs.Bytes(), []byte("panic recovered")) {
		t.Fatalf("logs = %s", logs.String())
	}
}
