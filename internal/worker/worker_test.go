package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/migration"
)

func TestWorkerSuccessCompletesAndProcessesDecodedMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := validPasswordSyncMessage()
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, want)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if processor.messages[0].UPN != want.UPN {
		t.Fatalf("processor UPN = %q, want %q", processor.messages[0].UPN, want.UPN)
	}
	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 || receiver.deadLettered != 0 {
		t.Fatalf("unexpected settlements: abandoned=%d deadLettered=%d", receiver.abandoned, receiver.deadLettered)
	}
}

func TestWorkerRetryableProcessorErrorAbandons(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onAbandon = cancel
	processor := &fakeProcessor{err: errors.New("graph temporarily unavailable")}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if receiver.abandoned != 1 {
		t.Fatalf("abandoned = %d, want 1", receiver.abandoned)
	}
	if receiver.completed != 0 || receiver.deadLettered != 0 {
		t.Fatalf("unexpected settlements: completed=%d deadLettered=%d", receiver.completed, receiver.deadLettered)
	}
}

func TestWorkerPermanentProcessorErrorDeadLettersWithFixedMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const password = "super-secret-password"
	msg := validPasswordSyncMessage()
	msg.Password = password
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onDeadLetter = cancel
	processor := &fakeProcessor{err: &PermanentError{
		Reason: "graph 403/password",
		Err:    errors.New("failed with " + password),
	}}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if receiver.deadLettered != 1 {
		t.Fatalf("deadLettered = %d, want 1", receiver.deadLettered)
	}
	if receiver.deadLetterReason != "permanent_processor_error" {
		t.Fatalf("deadLetterReason = %q, want permanent_processor_error", receiver.deadLetterReason)
	}
	if receiver.deadLetterDescription != deadLetterDescriptionPermanentError {
		t.Fatalf("deadLetterDescription = %q, want %q", receiver.deadLetterDescription, deadLetterDescriptionPermanentError)
	}
	if strings.Contains(receiver.deadLetterReason, password) || strings.Contains(receiver.deadLetterDescription, password) {
		t.Fatalf("dead-letter metadata contains password: reason=%q description=%q", receiver.deadLetterReason, receiver.deadLetterDescription)
	}
	if receiver.completed != 0 || receiver.abandoned != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
}

func TestWorkerPermanentProcessorErrorDoesNotTrustPasswordInReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const password = "super-secret-password"
	msg := validPasswordSyncMessage()
	msg.Password = password
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onDeadLetter = cancel
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReason("graph failed for " + msg.UPN + " with " + password),
		Err:    errors.New("processor failed"),
	}}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if receiver.deadLettered != 1 {
		t.Fatalf("deadLettered = %d, want 1", receiver.deadLettered)
	}
	if receiver.deadLetterReason != "permanent_processor_error" {
		t.Fatalf("deadLetterReason = %q, want permanent_processor_error", receiver.deadLetterReason)
	}
	if strings.Contains(receiver.deadLetterReason, password) || strings.Contains(receiver.deadLetterReason, msg.UPN) {
		t.Fatalf("dead-letter reason contains sensitive data: %q", receiver.deadLetterReason)
	}
	if strings.Contains(receiver.deadLetterDescription, password) || strings.Contains(receiver.deadLetterDescription, msg.UPN) {
		t.Fatalf("dead-letter description contains sensitive data: %q", receiver.deadLetterDescription)
	}
}

func TestWorkerInvalidMessagesDeadLetter(t *testing.T) {
	tests := []struct {
		name    string
		message *Message
	}{
		{
			name:    "invalid json",
			message: &Message{Kind: passwordSyncKind, Body: []byte("{")},
		},
		{
			name:    "wrong kind",
			message: &Message{Kind: "other", Body: mustMarshal(t, validPasswordSyncMessage())},
		},
		{
			name: "missing required field",
			message: &Message{
				Kind: passwordSyncKind,
				Body: mustMarshal(t, migration.PasswordSyncMessage{
					CN:         "u1234567",
					Password:   "secret",
					EnqueuedAt: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC),
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			receiver := &fakeReceiver{messages: []*Message{tt.message}}
			receiver.onDeadLetter = cancel
			processor := &fakeProcessor{}
			worker := newTestWorker(t, receiver, processor)

			if err := worker.Run(ctx); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			if processor.calls != 0 {
				t.Fatalf("processor calls = %d, want 0", processor.calls)
			}
			if receiver.deadLettered != 1 {
				t.Fatalf("deadLettered = %d, want 1", receiver.deadLettered)
			}
			if receiver.deadLetterReason != deadLetterReasonInvalidMessageSchema {
				t.Fatalf("deadLetterReason = %q, want %q", receiver.deadLetterReason, deadLetterReasonInvalidMessageSchema)
			}
			if receiver.deadLetterDescription != deadLetterDescriptionInvalidMessage {
				t.Fatalf("deadLetterDescription = %q, want %q", receiver.deadLetterDescription, deadLetterDescriptionInvalidMessage)
			}
		})
	}
}

func TestWorkerContextCancellationSkipsRemainingMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := workerMessage(t, validPasswordSyncMessage())
	second := workerMessage(t, validPasswordSyncMessage())
	receiver := &fakeReceiver{messages: []*Message{first, second}}
	processor := &fakeProcessor{afterCall: cancel}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
}

func TestWorkerUsesFreshSettlementContextAfterProcessorCancelsRunContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{
		messages:                    []*Message{workerMessage(t, validPasswordSyncMessage())},
		failSettlementsWhenCanceled: true,
	}
	receiver.onComplete = cancel
	processor := &fakeProcessor{afterCall: cancel}
	worker := newTestWorker(t, receiver, processor)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 || receiver.deadLettered != 0 {
		t.Fatalf("unexpected settlements: abandoned=%d deadLettered=%d", receiver.abandoned, receiver.deadLettered)
	}
}

func TestWorkerReturnsSettlementFailure(t *testing.T) {
	ctx := context.Background()
	completeErr := errors.New("service bus complete failed")
	receiver := &fakeReceiver{
		messages:    []*Message{workerMessage(t, validPasswordSyncMessage())},
		completeErr: completeErr,
	}
	processor := &fakeProcessor{}
	worker := newTestWorker(t, receiver, processor)

	err := worker.Run(ctx)
	if err == nil {
		t.Fatal("Run returned nil error")
	}
	if !errors.Is(err, completeErr) {
		t.Fatalf("Run error = %v, want complete error", err)
	}
	if !strings.Contains(err.Error(), "complete worker message") {
		t.Fatalf("Run error = %q, want complete worker message", err.Error())
	}
}

func TestNewValidatesDependencies(t *testing.T) {
	processor := &fakeProcessor{}
	receiver := &fakeReceiver{}

	if _, err := New(nil, processor, Options{}); err == nil || err.Error() != "worker receiver is required" {
		t.Fatalf("New with nil receiver error = %v", err)
	}
	if _, err := New(receiver, nil, Options{}); err == nil || err.Error() != "worker processor is required" {
		t.Fatalf("New with nil processor error = %v", err)
	}
}

func newTestWorker(t *testing.T, receiver Receiver, processor Processor) *Worker {
	t.Helper()

	worker, err := New(receiver, processor, Options{MaxMessages: 10})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return worker
}

func workerMessage(t *testing.T, msg migration.PasswordSyncMessage) *Message {
	t.Helper()
	return &Message{
		Kind: passwordSyncKind,
		Body: mustMarshal(t, msg),
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	return body
}

func validPasswordSyncMessage() migration.PasswordSyncMessage {
	return migration.PasswordSyncMessage{
		CN:          "u1234567",
		UPN:         "u1234567@example.edu",
		Password:    "secret",
		DisplayName: "Test User",
		Mail:        "test@example.edu",
		EnqueuedAt:  time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC),
	}
}

type fakeReceiver struct {
	messages []*Message

	completed    int
	abandoned    int
	deadLettered int

	deadLetterReason      string
	deadLetterDescription string

	completeErr   error
	abandonErr    error
	deadLetterErr error

	onComplete   func()
	onAbandon    func()
	onDeadLetter func()

	failSettlementsWhenCanceled bool
}

func (r *fakeReceiver) ReceiveMessages(ctx context.Context, maxMessages int) ([]*Message, error) {
	if len(r.messages) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if len(r.messages) <= maxMessages {
		messages := r.messages
		r.messages = nil
		return messages, nil
	}
	messages := r.messages[:maxMessages]
	r.messages = r.messages[maxMessages:]
	return messages, nil
}

func (r *fakeReceiver) CompleteMessage(ctx context.Context, msg *Message) error {
	if r.failSettlementsWhenCanceled {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	r.completed++
	if r.onComplete != nil {
		r.onComplete()
	}
	return r.completeErr
}

func (r *fakeReceiver) AbandonMessage(ctx context.Context, msg *Message) error {
	if r.failSettlementsWhenCanceled {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	r.abandoned++
	if r.onAbandon != nil {
		r.onAbandon()
	}
	return r.abandonErr
}

func (r *fakeReceiver) DeadLetterMessage(ctx context.Context, msg *Message, reason string, description string) error {
	if r.failSettlementsWhenCanceled {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	r.deadLettered++
	r.deadLetterReason = reason
	r.deadLetterDescription = description
	if r.onDeadLetter != nil {
		r.onDeadLetter()
	}
	return r.deadLetterErr
}

type fakeProcessor struct {
	calls     int
	messages  []migration.PasswordSyncMessage
	err       error
	afterCall func()
}

func (p *fakeProcessor) ProcessPasswordSync(ctx context.Context, msg migration.PasswordSyncMessage) error {
	p.calls++
	p.messages = append(p.messages, msg)
	if p.afterCall != nil {
		p.afterCall()
	}
	return p.err
}
