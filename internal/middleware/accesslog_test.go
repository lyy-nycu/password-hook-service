package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nycu/password-hook-service/internal/requestid"
)

func TestAccessLogWritesTraceIDAndStatus(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	output := buf.String()
	for _, want := range []string{`"traceId":"trace-123"`, `"status":202`, `"path":"/api/v1/hook/password"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output %q does not contain %s", output, want)
		}
	}
}
