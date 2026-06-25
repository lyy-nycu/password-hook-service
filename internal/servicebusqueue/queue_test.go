package servicebusqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
)

func TestQueueSendsPasswordSyncMessageWithTTL(t *testing.T) {
	ctx := context.Background()
	sender := &captureSender{}
	queue := New(sender, 300*time.Second)
	enqueuedAt := time.Date(2026, 6, 25, 12, 30, 0, 123, time.FixedZone("TST", 8*60*60))
	msg := migration.PasswordSyncMessage{
		CN:          "u1234567",
		UPN:         "u1234567@example.edu",
		Password:    "not-for-metadata",
		DisplayName: "Test User",
		Mail:        "test@example.edu",
		EnqueuedAt:  enqueuedAt,
	}

	if err := queue.EnqueuePasswordSync(ctx, msg); err != nil {
		t.Fatalf("EnqueuePasswordSync returned error: %v", err)
	}

	if sender.sent != 1 {
		t.Fatalf("sent %d messages, want 1", sender.sent)
	}
	got := sender.message
	if got == nil {
		t.Fatal("sent message is nil")
	}
	if got.TimeToLive == nil || *got.TimeToLive != 300*time.Second {
		t.Fatalf("TimeToLive = %v, want 300s", got.TimeToLive)
	}
	if got.ContentType == nil || *got.ContentType != "application/json" {
		t.Fatalf("ContentType = %v, want application/json", got.ContentType)
	}
	if got.Subject == nil || *got.Subject != "password-sync" {
		t.Fatalf("Subject = %v, want password-sync", got.Subject)
	}
	wantMessageID := fmt.Sprintf("%s:%s", msg.UPN, msg.EnqueuedAt.UTC().Format(time.RFC3339Nano))
	if got.MessageID == nil || *got.MessageID != wantMessageID {
		t.Fatalf("MessageID = %v, want %q", got.MessageID, wantMessageID)
	}

	var body migration.PasswordSyncMessage
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatalf("message body is not JSON PasswordSyncMessage: %v", err)
	}
	if body.UPN != msg.UPN {
		t.Fatalf("body UPN = %q, want %q", body.UPN, msg.UPN)
	}
	if body.Password != msg.Password {
		t.Fatalf("body Password = %q, want %q", body.Password, msg.Password)
	}

	assertPasswordSyncMetadata(t, got.ApplicationProperties, msg)
	assertNoPasswordMetadata(t, got.ApplicationProperties, msg.Password)
}

func TestQueuePropagatesSendError(t *testing.T) {
	sendErr := errors.New("service bus unavailable")
	queue := New(&captureSender{sendErr: sendErr}, 300*time.Second)

	err := queue.EnqueuePasswordSync(context.Background(), migration.PasswordSyncMessage{
		CN:         "u1234567",
		UPN:        "u1234567@example.edu",
		Password:   "secret",
		EnqueuedAt: time.Date(2026, 6, 25, 4, 30, 0, 0, time.UTC),
	})

	if err == nil {
		t.Fatal("EnqueuePasswordSync returned nil error")
	}
	if !strings.Contains(err.Error(), "send password sync message") {
		t.Fatalf("error = %q, want send password sync message", err.Error())
	}
	if !errors.Is(err, sendErr) {
		t.Fatalf("error does not wrap send error: %v", err)
	}
}

func TestQueueWrapsMarshalErrorAndDoesNotSend(t *testing.T) {
	sender := &captureSender{}
	queue := New(sender, 300*time.Second)

	err := queue.EnqueuePasswordSync(context.Background(), migration.PasswordSyncMessage{
		CN:         "u1234567",
		UPN:        "u1234567@example.edu",
		Password:   "secret",
		EnqueuedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	if err == nil {
		t.Fatal("EnqueuePasswordSync returned nil error")
	}
	if !strings.Contains(err.Error(), "marshal password sync message") {
		t.Fatalf("error = %q, want marshal password sync message", err.Error())
	}
	if !strings.Contains(err.Error(), "year outside of range") {
		t.Fatalf("error = %q, want underlying marshal failure", err.Error())
	}
	if sender.sent != 0 {
		t.Fatalf("sent %d messages after marshal error, want 0", sender.sent)
	}
}

func TestNewFromConnectionStringWrapsClientError(t *testing.T) {
	queue, err := NewFromConnectionString("not a service bus connection string", "password-sync", 300*time.Second)

	if err == nil {
		t.Fatal("NewFromConnectionString returned nil error")
	}
	if queue != nil {
		t.Fatalf("NewFromConnectionString queue = %#v, want nil", queue)
	}
	if !strings.Contains(err.Error(), "create service bus client") {
		t.Fatalf("error = %q, want create service bus client", err.Error())
	}
}

func TestQueueCloseClosesSenderAndClient(t *testing.T) {
	ctx := context.Background()
	sender := &captureSender{}
	client := &captureCloser{}
	queue := NewWithClient(sender, client, 300*time.Second)

	if err := queue.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if sender.closed != 1 {
		t.Fatalf("sender closed %d times, want 1", sender.closed)
	}
	if client.closed != 1 {
		t.Fatalf("client closed %d times, want 1", client.closed)
	}

	senderErr := errors.New("close sender")
	sender = &captureSender{closeErr: senderErr}
	client = &captureCloser{}
	queue = NewWithClient(sender, client, 300*time.Second)

	err := queue.Close(ctx)
	if !errors.Is(err, senderErr) {
		t.Fatalf("Close error = %v, want sender error", err)
	}
	if !strings.Contains(err.Error(), "close service bus sender") {
		t.Fatalf("Close error = %q, want close service bus sender", err.Error())
	}
	if client.closed != 1 {
		t.Fatalf("client closed %d times after sender error, want 1", client.closed)
	}

	clientErr := errors.New("close client")
	sender = &captureSender{}
	client = &captureCloser{closeErr: clientErr}
	queue = NewWithClient(sender, client, 300*time.Second)

	err = queue.Close(ctx)
	if !errors.Is(err, clientErr) {
		t.Fatalf("Close error = %v, want client error", err)
	}
	if !strings.Contains(err.Error(), "close service bus client") {
		t.Fatalf("Close error = %q, want close service bus client", err.Error())
	}
	if sender.closed != 1 {
		t.Fatalf("sender closed %d times before client error, want 1", sender.closed)
	}

	client = &captureCloser{}
	queue = NewWithClient(nil, client, 300*time.Second)

	if err := queue.Close(ctx); err != nil {
		t.Fatalf("Close with nil sender returned error: %v", err)
	}
	if client.closed != 1 {
		t.Fatalf("client closed %d times with nil sender, want 1", client.closed)
	}
}

type captureSender struct {
	message  *azservicebus.Message
	sent     int
	closed   int
	sendErr  error
	closeErr error
}

func (s *captureSender) SendMessage(ctx context.Context, msg *azservicebus.Message, options *azservicebus.SendMessageOptions) error {
	s.sent++
	s.message = msg
	return s.sendErr
}

func (s *captureSender) Close(ctx context.Context) error {
	s.closed++
	return s.closeErr
}

type captureCloser struct {
	closed   int
	closeErr error
}

func (c *captureCloser) Close(ctx context.Context) error {
	c.closed++
	return c.closeErr
}

func assertPasswordSyncMetadata(t *testing.T, props map[string]any, msg migration.PasswordSyncMessage) {
	t.Helper()

	if len(props) != 3 {
		t.Fatalf("application properties length = %d, want 3: %#v", len(props), props)
	}
	if props["kind"] != "password-sync" {
		t.Fatalf("kind property = %v, want password-sync", props["kind"])
	}
	if props["cn"] != msg.CN {
		t.Fatalf("cn property = %v, want %q", props["cn"], msg.CN)
	}
	if props["upn"] != msg.UPN {
		t.Fatalf("upn property = %v, want %q", props["upn"], msg.UPN)
	}
}

func assertNoPasswordMetadata(t *testing.T, props map[string]any, password string) {
	t.Helper()

	for key, value := range props {
		if strings.Contains(strings.ToLower(key), "password") {
			t.Fatalf("application property %q contains password metadata", key)
		}
		if text, ok := value.(string); ok && strings.Contains(text, password) {
			t.Fatalf("application property %q contains password-like value", key)
		}
	}
}
