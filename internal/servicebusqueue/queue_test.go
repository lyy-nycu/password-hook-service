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
	"github.com/nycu/password-hook-service/internal/worker"
)

func TestQueueSendsPasswordSyncMessageWithTTL(t *testing.T) {
	ctx := context.Background()
	sender := &captureSender{}
	queue := mustNewQueue(t, sender, 300*time.Second)
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
	queue := mustNewQueue(t, &captureSender{sendErr: sendErr}, 300*time.Second)

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
	queue := mustNewQueue(t, sender, 300*time.Second)

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

func TestNewReceiverFromConnectionStringWrapsReceiverError(t *testing.T) {
	receiver, err := NewReceiverFromConnectionString(validConnectionString(), "")

	if err == nil {
		t.Fatal("NewReceiverFromConnectionString returned nil error")
	}
	if receiver != nil {
		t.Fatalf("NewReceiverFromConnectionString receiver = %#v, want nil", receiver)
	}
	if !strings.Contains(err.Error(), "create service bus receiver") {
		t.Fatalf("error = %q, want create service bus receiver", err.Error())
	}
}

func TestReceiverReceivesAndSettlesServiceBusMessage(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"cn":"u1234567","upn":"u1234567@example.edu","password":"secret","enqueuedAt":"2026-06-27T12:00:00Z"}`)
	native := &azservicebus.ReceivedMessage{
		ApplicationProperties: map[string]any{"kind": "password-sync"},
		Body:                  body,
	}
	serviceBusReceiver := &captureServiceBusReceiver{messages: []*azservicebus.ReceivedMessage{native}}
	receiver := NewReceiver(serviceBusReceiver)

	messages, err := receiver.ReceiveMessages(ctx, 1)
	if err != nil {
		t.Fatalf("ReceiveMessages returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("received %d messages, want 1", len(messages))
	}
	if messages[0].Kind != "password-sync" {
		t.Fatalf("message kind = %q, want password-sync", messages[0].Kind)
	}
	if string(messages[0].Body) != string(body) {
		t.Fatalf("message body = %q, want %q", messages[0].Body, body)
	}

	if err := receiver.CompleteMessage(ctx, messages[0]); err != nil {
		t.Fatalf("CompleteMessage returned error: %v", err)
	}
	if serviceBusReceiver.completed != native {
		t.Fatalf("completed native message = %#v, want %#v", serviceBusReceiver.completed, native)
	}
}

func TestReceiverAbandonsServiceBusMessage(t *testing.T) {
	ctx := context.Background()
	native := &azservicebus.ReceivedMessage{
		ApplicationProperties: map[string]any{"kind": "password-sync"},
		Body:                  []byte(`{"cn":"u1234567","upn":"u1234567@example.edu","password":"secret","enqueuedAt":"2026-06-27T12:00:00Z"}`),
	}
	serviceBusReceiver := &captureServiceBusReceiver{messages: []*azservicebus.ReceivedMessage{native}}
	receiver := NewReceiver(serviceBusReceiver)

	messages, err := receiver.ReceiveMessages(ctx, 1)
	if err != nil {
		t.Fatalf("ReceiveMessages returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("received %d messages, want 1", len(messages))
	}

	if err := receiver.AbandonMessage(ctx, messages[0]); err != nil {
		t.Fatalf("AbandonMessage returned error: %v", err)
	}

	if serviceBusReceiver.abandoned != native {
		t.Fatalf("abandoned native message = %#v, want %#v", serviceBusReceiver.abandoned, native)
	}
	if serviceBusReceiver.completed != nil {
		t.Fatalf("unexpected completed settlement: %#v", serviceBusReceiver.completed)
	}
}

func TestReceiverRejectsMessageNotReceivedByReceiver(t *testing.T) {
	ctx := context.Background()
	receiver := NewReceiver(&captureServiceBusReceiver{})
	msg := &worker.Message{Kind: "password-sync", Body: []byte("{}")}

	tests := []struct {
		name   string
		settle func() error
	}{
		{
			name:   "complete",
			settle: func() error { return receiver.CompleteMessage(ctx, msg) },
		},
		{
			name:   "abandon",
			settle: func() error { return receiver.AbandonMessage(ctx, msg) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.settle()
			if err == nil {
				t.Fatal("settlement returned nil error")
			}
			if err.Error() != "worker message was not received by this service bus receiver" {
				t.Fatalf("settlement error = %q, want worker message ownership error", err.Error())
			}
		})
	}
}

func TestReceiverWithNilNativeReceiverReturnsErrors(t *testing.T) {
	ctx := context.Background()
	receiver := NewReceiver(nil)
	msg := &worker.Message{Kind: "password-sync", Body: []byte("{}")}

	if _, err := receiver.ReceiveMessages(ctx, 1); err == nil || err.Error() != "service bus receiver is required" {
		t.Fatalf("ReceiveMessages error = %v, want service bus receiver is required", err)
	}
	if err := receiver.CompleteMessage(ctx, msg); err == nil || err.Error() != "service bus receiver is required" {
		t.Fatalf("CompleteMessage error = %v, want service bus receiver is required", err)
	}
	if err := receiver.AbandonMessage(ctx, msg); err == nil || err.Error() != "service bus receiver is required" {
		t.Fatalf("AbandonMessage error = %v, want service bus receiver is required", err)
	}
}

func TestReceiverCloseClosesReceiverAndClient(t *testing.T) {
	ctx := context.Background()
	serviceBusReceiver := &captureServiceBusReceiver{}
	client := &captureCloser{}
	receiver := NewReceiverWithClient(serviceBusReceiver, client)

	if err := receiver.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if serviceBusReceiver.closed != 1 {
		t.Fatalf("receiver closed %d times, want 1", serviceBusReceiver.closed)
	}
	if client.closed != 1 {
		t.Fatalf("client closed %d times, want 1", client.closed)
	}

	receiverErr := errors.New("close receiver")
	clientErr := errors.New("close client")
	serviceBusReceiver = &captureServiceBusReceiver{closeErr: receiverErr}
	client = &captureCloser{closeErr: clientErr}
	receiver = NewReceiverWithClient(serviceBusReceiver, client)

	err := receiver.Close(ctx)
	if !errors.Is(err, receiverErr) {
		t.Fatalf("Close error = %v, want receiver error", err)
	}
	if !errors.Is(err, clientErr) {
		t.Fatalf("Close error = %v, want client error", err)
	}
	if !strings.Contains(err.Error(), "close service bus receiver") {
		t.Fatalf("Close error = %q, want close service bus receiver", err.Error())
	}
	if !strings.Contains(err.Error(), "close service bus client") {
		t.Fatalf("Close error = %q, want close service bus client", err.Error())
	}
}

func TestNewRejectsNilSender(t *testing.T) {
	queue, err := New(nil, 300*time.Second)

	if err == nil {
		t.Fatal("New returned nil error for nil sender")
	}
	if queue != nil {
		t.Fatalf("New queue = %#v, want nil", queue)
	}
	if err.Error() != "service bus sender is required" {
		t.Fatalf("New error = %q, want service bus sender is required", err.Error())
	}
}

func TestNewWithClientRejectsNilSender(t *testing.T) {
	queue, err := NewWithClient(nil, &captureCloser{}, 300*time.Second)

	if err == nil {
		t.Fatal("NewWithClient returned nil error for nil sender")
	}
	if queue != nil {
		t.Fatalf("NewWithClient queue = %#v, want nil", queue)
	}
	if err.Error() != "service bus sender is required" {
		t.Fatalf("NewWithClient error = %q, want service bus sender is required", err.Error())
	}
}

func TestCloseWithTimeoutUsesBoundedUncanceledContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	closer := &captureCloser{}

	err := closeWithTimeout(parent, closer)

	if err != nil {
		t.Fatalf("closeWithTimeout returned error: %v", err)
	}
	if closer.closed != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closed)
	}
	if closer.closeErrs[0] != nil {
		t.Fatalf("close context err = %v, want nil", closer.closeErrs[0])
	}
	if !closer.closeHadDeadlines[0] {
		t.Fatal("close context has no deadline")
	}
}

func TestQueueCloseClosesSenderAndClient(t *testing.T) {
	ctx := context.Background()
	sender := &captureSender{}
	client := &captureCloser{}
	queue := mustNewQueueWithClient(t, sender, client, 300*time.Second)

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
	queue = mustNewQueueWithClient(t, sender, client, 300*time.Second)

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
	queue = mustNewQueueWithClient(t, sender, client, 300*time.Second)

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

	senderErr = errors.New("close sender")
	clientErr = errors.New("close client")
	sender = &captureSender{closeErr: senderErr}
	client = &captureCloser{closeErr: clientErr}
	queue = mustNewQueueWithClient(t, sender, client, 300*time.Second)

	err = queue.Close(ctx)
	if !errors.Is(err, senderErr) {
		t.Fatalf("Close error = %v, want sender error", err)
	}
	if !errors.Is(err, clientErr) {
		t.Fatalf("Close error = %v, want client error", err)
	}
	if !strings.Contains(err.Error(), "close service bus sender") {
		t.Fatalf("Close error = %q, want close service bus sender", err.Error())
	}
	if !strings.Contains(err.Error(), "close service bus client") {
		t.Fatalf("Close error = %q, want close service bus client", err.Error())
	}

}

func mustNewQueue(t *testing.T, sender sender, ttl time.Duration) *Queue {
	t.Helper()
	queue, err := New(sender, ttl)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return queue
}

func mustNewQueueWithClient(t *testing.T, sender sender, client closer, ttl time.Duration) *Queue {
	t.Helper()
	queue, err := NewWithClient(sender, client, ttl)
	if err != nil {
		t.Fatalf("NewWithClient returned error: %v", err)
	}
	return queue
}

type captureServiceBusReceiver struct {
	messages []*azservicebus.ReceivedMessage

	completed *azservicebus.ReceivedMessage
	abandoned *azservicebus.ReceivedMessage
	closed    int
	closeErr  error
}

func (r *captureServiceBusReceiver) ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	if len(r.messages) == 0 {
		return nil, nil
	}
	if len(r.messages) <= maxMessages {
		messages := r.messages
		return messages, nil
	}
	return r.messages[:maxMessages], nil
}

func (r *captureServiceBusReceiver) CompleteMessage(ctx context.Context, msg *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error {
	r.completed = msg
	return nil
}

func (r *captureServiceBusReceiver) AbandonMessage(ctx context.Context, msg *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error {
	r.abandoned = msg
	return nil
}

func (r *captureServiceBusReceiver) Close(ctx context.Context) error {
	r.closed++
	return r.closeErr
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
	closed            int
	closeErr          error
	closeHadDeadlines []bool
	closeErrs         []error
}

func (c *captureCloser) Close(ctx context.Context) error {
	c.closed++
	_, hasDeadline := ctx.Deadline()
	c.closeHadDeadlines = append(c.closeHadDeadlines, hasDeadline)
	c.closeErrs = append(c.closeErrs, ctx.Err())
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

func validConnectionString() string {
	return "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA=="
}
