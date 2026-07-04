package migration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

func TestServiceEncryptsPasswordBeforeEnqueue(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := passwordcrypto.NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}
	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, codec)
	service.now = func() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) }

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    []byte("cleartext-password"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	got := queue.messages[0]
	if got.Password != "" {
		t.Fatalf("queued Password = %q, want empty", got.Password)
	}
	if got.PasswordCiphertext == "" || got.PasswordNonce == "" || got.PasswordKeyID != "password-payload-key-v1" || got.PasswordAlg != passwordcrypto.AlgorithmAES256GCM {
		t.Fatalf("queued encrypted fields are invalid: %#v", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "cleartext-password") || strings.Contains(string(body), `"password"`) {
		t.Fatalf("queued JSON leaks password: %s", body)
	}
}

func TestServiceZerosPasswordAfterSuccessfulEnqueue(t *testing.T) {
	t.Parallel()

	password := []byte("cleartext-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	assertZeroedBytes(t, password, "request password after successful enqueue")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after successful enqueue")
}

func TestServiceZerosPasswordWhenSkippingExternalEmail(t *testing.T) {
	t.Parallel()

	password := []byte("external-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)

	decision, err := service.Submit(context.Background(), Request{
		CN:          "guest@gmail.com",
		Password:    password,
		DisplayName: "Guest",
		Mail:        "guest@gmail.com",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Skipped {
		t.Fatal("decision.Skipped = false, want true")
	}
	if len(encrypter.password) != 0 {
		t.Fatalf("encrypter was called for skipped external identity")
	}
	assertZeroedBytes(t, password, "request password after external skip")
}

func TestServiceZerosPasswordWhenEncryptFails(t *testing.T) {
	t.Parallel()

	password := []byte("encrypt-failure-password")
	encryptErr := errors.New("encrypt failed")
	service := NewService("nycu.edu.tw", &captureQueue{}, failingEncrypter{err: encryptErr})

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, encryptErr) {
		t.Fatalf("Submit error = %v, want encrypt error", err)
	}
	assertZeroedBytes(t, password, "request password after encrypt failure")
}

func TestServiceZerosPasswordWhenQueueFails(t *testing.T) {
	t.Parallel()

	password := []byte("queue-failure-password")
	queueErr := errors.New("queue unavailable")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", failingQueue{err: queueErr}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, queueErr) {
		t.Fatalf("Submit error = %v, want queue error", err)
	}
	assertZeroedBytes(t, password, "request password after queue failure")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after queue failure")
}

type captureQueue struct {
	messages []PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

type captureEncrypter struct {
	password []byte
}

func (e *captureEncrypter) Encrypt(_ context.Context, password []byte, _ []byte) (passwordcrypto.Envelope, error) {
	e.password = password
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}

type failingEncrypter struct {
	err error
}

func (e failingEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{}, e.err
}

type failingQueue struct {
	err error
}

func (q failingQueue) EnqueuePasswordSync(context.Context, PasswordSyncMessage) error {
	return q.err
}

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}
