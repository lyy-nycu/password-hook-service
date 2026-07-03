package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHookZerosDecodedPasswordAfterSubmit(t *testing.T) {
	t.Parallel()

	encrypter := &capturePasswordEncrypter{}
	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, encrypter)
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"cleartext-password","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(encrypter.password) == 0 {
		t.Fatal("encrypter did not see password bytes")
	}
	assertZeroedBytes(t, encrypter.password, "decoded hook password after submit")
}

func TestHookDecodesEscapedPasswordIntoBytes(t *testing.T) {
	t.Parallel()

	var body passwordHookRequest
	err := json.Unmarshal([]byte(`{"cn":"311551001","password":"pa\"ss\nword","displayName":"Student","mail":"student@nycu.edu.tw"}`), &body)

	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got := string(body.Password); got != "pa\"ss\nword" {
		t.Fatalf("password = %q, want escaped password decoded", got)
	}
	passwordcrypto.ZeroBytes(body.Password)
	assertZeroedBytes(t, body.Password, "decoded escaped password")
}

func TestHookInvalidJSONDoesNotEchoPassword(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", strings.NewReader(`{"cn":"311551001","password":"cleartext-password"`))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if strings.Contains(rec.Body.String(), "cleartext-password") {
		t.Fatalf("problem response leaked password: %s", rec.Body.String())
	}
}

func TestHookZerosRawBodyWhenReadFails(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	requestBody := &readErrorBody{data: []byte(`{"cn":"311551001","password":"cleartext-password"`)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.Body = requestBody

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if len(requestBody.observed) == 0 {
		t.Fatal("read error body did not expose bytes to handler")
	}
	assertZeroedBytes(t, requestBody.observed, "raw hook request body after read error")
}

func TestPasswordBytesZerosPreviousValueOnOverwrite(t *testing.T) {
	t.Parallel()

	var password passwordBytes
	if err := password.UnmarshalJSON([]byte(`"first-password"`)); err != nil {
		t.Fatalf("UnmarshalJSON first password returned error: %v", err)
	}
	first := []byte(password)

	if err := password.UnmarshalJSON([]byte(`"second-password"`)); err != nil {
		t.Fatalf("UnmarshalJSON second password returned error: %v", err)
	}

	if got := string(password); got != "second-password" {
		t.Fatalf("password = %q, want second password", got)
	}
	assertZeroedBytes(t, first, "previous decoded password after overwrite")
	passwordcrypto.ZeroBytes(password)
}

func TestPasswordBytesZerosPreviousValueWhenDecodeFails(t *testing.T) {
	t.Parallel()

	var password passwordBytes
	if err := password.UnmarshalJSON([]byte(`"first-password"`)); err != nil {
		t.Fatalf("UnmarshalJSON first password returned error: %v", err)
	}
	first := []byte(password)

	if err := password.UnmarshalJSON([]byte(`123`)); err == nil {
		t.Fatal("UnmarshalJSON invalid password returned nil error")
	}

	assertZeroedBytes(t, first, "previous decoded password after decode failure")
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

type capturePasswordEncrypter struct {
	password []byte
}

func (e *capturePasswordEncrypter) Encrypt(_ context.Context, password []byte, _ []byte) (passwordcrypto.Envelope, error) {
	e.password = password
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}

type readErrorBody struct {
	data     []byte
	observed []byte
}

func (b *readErrorBody) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	b.observed = p[:n]
	return n, errors.New("read failed")
}

func (b *readErrorBody) Close() error {
	return nil
}

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}
