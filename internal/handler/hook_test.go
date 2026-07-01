package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

func TestHookEnqueuesInternalStudentID(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	if queue.messages[0].UPN != "311551001@nycu.edu.tw" {
		t.Fatalf("queued upn = %q", queue.messages[0].UPN)
	}
}

func TestHookSkipsExternalEmailIdentity(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
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

func TestHookRejectsUnknownCNAsBadRequest(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"bad cn!","password":"secret","displayName":"Bad","mail":"bad@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHookQueueFailureReturnsInternalError(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", failingQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHookValidationProblemIncludesTraceID(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var bodyProblem problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyProblem); err != nil {
		t.Fatalf("problem response is not valid json: %v", err)
	}
	if bodyProblem.TraceID != "trace-123" {
		t.Fatalf("traceId = %q, want trace-123", bodyProblem.TraceID)
	}
}

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

type failingQueue struct{}

func (failingQueue) EnqueuePasswordSync(context.Context, migration.PasswordSyncMessage) error {
	return errors.New("queue unavailable")
}

type fakePasswordEncrypter struct{}

func (fakePasswordEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}
