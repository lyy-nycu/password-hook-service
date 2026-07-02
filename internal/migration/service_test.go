package migration

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
		Password:    "cleartext-password",
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

type captureQueue struct {
	messages []PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}
