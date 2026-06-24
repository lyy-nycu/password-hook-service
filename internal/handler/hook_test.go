package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nycu/password-hook-service/internal/migration"
)

func TestHookSkipsExternalEmailIdentity(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue)
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(queue.messages) != 0 {
		t.Fatalf("queued %d messages, want 0", len(queue.messages))
	}
}

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}
