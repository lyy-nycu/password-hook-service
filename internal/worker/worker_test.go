package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

func TestWorkerSuccessCompletesAndProcessesDecryptedMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := validPasswordSyncMessage()
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, want)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{}
	decrypter := &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}
	worker := newTestWorker(t, receiver, processor, decrypter, &fakeDeadLetterSink{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if decrypter.calls != 1 {
		t.Fatalf("decrypter calls = %d, want 1", decrypter.calls)
	}
	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if processor.messages[0].UPN != want.UPN {
		t.Fatalf("processor UPN = %q, want %q", processor.messages[0].UPN, want.UPN)
	}
	if processor.messages[0].Password != "cleartext-password" {
		t.Fatalf("processor Password = %q, want cleartext-password", processor.messages[0].Password)
	}
	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 {
		t.Fatalf("abandoned = %d, want 0", receiver.abandoned)
	}
}

func TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := []byte(`{"cn":"u1234567","upn":"u1234567@example.edu","passwordCiphertext":"ciphertext","passwordNonce":"nonce"}`)
	receiver := &fakeReceiver{messages: []*Message{{Kind: passwordSyncKind, Body: body}}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{}, deadLetters)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls)
	}
	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	entry := deadLetters.entries[0]
	if entry.Reason != DeadLetterReasonInvalidMessageSchema {
		t.Fatalf("safe DLQ reason = %q, want %q", entry.Reason, DeadLetterReasonInvalidMessageSchema)
	}
	if entry.CN != "u1234567" || entry.UPN != "u1234567@example.edu" {
		t.Fatalf("safe DLQ identity = (%q, %q), want parsed CN and UPN", entry.CN, entry.UPN)
	}
	assertSafeDLQEntry(t, entry)
	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 {
		t.Fatalf("abandoned = %d, want 0", receiver.abandoned)
	}
}

func TestWorkerRetriesTransientProcessorErrorsBeforeSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{errs: []error{
		errors.New("graph temporarily unavailable"),
		errors.New("graph still unavailable"),
		nil,
	}}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	sleeper := &fakeSleeper{}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, sleeper)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 3 {
		t.Fatalf("processor calls = %d, want 3", processor.calls)
	}
	if len(sleeper.durations) != 2 || sleeper.durations[0] != time.Second || sleeper.durations[1] != 2*time.Second {
		t.Fatalf("sleeps = %v, want [1s 2s]", sleeper.durations)
	}
	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 {
		t.Fatalf("abandoned = %d, want 0", receiver.abandoned)
	}
	if len(deadLetters.entries) != 0 {
		t.Fatalf("safe DLQ entries = %d, want 0", len(deadLetters.entries))
	}
}

func TestWorkerRetriesTransientProcessorErrorsThenSafeDLQ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{err: errors.New("graph temporarily unavailable")}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	sleeper := &fakeSleeper{}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, sleeper)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 4 {
		t.Fatalf("processor calls = %d, want 4", processor.calls)
	}
	wantSleeps := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(sleeper.durations) != len(wantSleeps) {
		t.Fatalf("sleeps = %v, want %v", sleeper.durations, wantSleeps)
	}
	for i, want := range wantSleeps {
		if sleeper.durations[i] != want {
			t.Fatalf("sleeps[%d] = %s, want %s", i, sleeper.durations[i], want)
		}
	}
	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	entry := deadLetters.entries[0]
	if entry.Reason != DeadLetterReasonTransientRetriesExhausted {
		t.Fatalf("safe DLQ reason = %q, want %q", entry.Reason, DeadLetterReasonTransientRetriesExhausted)
	}
	if entry.Attempts != 4 {
		t.Fatalf("safe DLQ attempts = %d, want 4", entry.Attempts)
	}
	assertSafeDLQEntry(t, entry)
	if receiver.completed != 1 || receiver.abandoned != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
}

func TestWorkerPermanentProcessorErrorSkipsRetryAndSafeDLQ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReasonProcessorError,
		Err:    errors.New("graph 403"),
	}}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	sleeper := &fakeSleeper{}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, sleeper)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if len(sleeper.durations) != 0 {
		t.Fatalf("sleeps = %v, want none", sleeper.durations)
	}
	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	entry := deadLetters.entries[0]
	if entry.Reason != DeadLetterReasonPermanentProcessor {
		t.Fatalf("safe DLQ reason = %q, want %q", entry.Reason, DeadLetterReasonPermanentProcessor)
	}
	if entry.Attempts != 1 {
		t.Fatalf("safe DLQ attempts = %d, want 1", entry.Attempts)
	}
	assertSafeDLQEntry(t, entry)
	if receiver.completed != 1 || receiver.abandoned != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
}

func TestWorkerPermanentProcessorErrorDoesNotTrustSensitiveReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const password = "super-secret-password"
	msg := validPasswordSyncMessage()
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReason("graph failed for " + msg.UPN + " with " + password),
		Err:    errors.New("processor failed"),
	}}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte(password)}, deadLetters, &fakeSleeper{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	entry := deadLetters.entries[0]
	if entry.Reason != DeadLetterReasonPermanentProcessor {
		t.Fatalf("safe DLQ reason = %q, want %q", entry.Reason, DeadLetterReasonPermanentProcessor)
	}
	if strings.Contains(entry.Reason, password) || strings.Contains(entry.Reason, msg.UPN) {
		t.Fatalf("safe DLQ reason contains sensitive data: %q", entry.Reason)
	}
	if strings.Contains(entry.Description, password) || strings.Contains(entry.Description, msg.UPN) {
		t.Fatalf("safe DLQ description contains sensitive data: %q", entry.Description)
	}
}

func TestWorkerAbandonsWhenRetryBackoffIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onAbandon = cancel
	processor := &fakeProcessor{err: errors.New("graph temporarily unavailable")}
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	sleeper := &fakeSleeper{err: context.Canceled}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, sleeper)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if receiver.abandoned != 1 || receiver.completed != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
	if len(deadLetters.entries) != 0 {
		t.Fatalf("safe DLQ entries = %d, want 0", len(deadLetters.entries))
	}
}

func TestWorkerAbandonsOriginalWhenSafeDLQWriteFails(t *testing.T) {
	ctx := context.Background()
	sinkErr := errors.New("safe DLQ unavailable")
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReasonProcessorError,
		Err:    errors.New("graph 403"),
	}}
	deadLetters := &fakeDeadLetterSink{err: sinkErr}
	worker := newPolicyTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, &fakeSleeper{})

	err := worker.Run(ctx)
	if err == nil {
		t.Fatal("Run returned nil error")
	}
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Run error = %v, want safe DLQ error", err)
	}
	if receiver.abandoned != 1 || receiver.completed != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
}

func TestWorkerDoesNotRetainPlaintextDuringRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	decrypter := &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}
	sleeper := &fakeSleeper{
		onSleep: func() {
			for _, plaintext := range decrypter.returnedPlaintexts {
				if bytes.Contains(plaintext, []byte("cleartext-password")) {
					t.Fatalf("plaintext was not cleared before retry backoff: %q", plaintext)
				}
			}
		},
	}
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{errs: []error{errors.New("retry"), nil}}
	worker := newPolicyTestWorker(t, receiver, processor, decrypter, &fakeDeadLetterSink{}, sleeper)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestDecodePasswordSyncMessageRejectsMissingEncryptedFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*migration.PasswordSyncMessage)
		wantErr string
	}{
		{
			name:    "ciphertext",
			mutate:  func(msg *migration.PasswordSyncMessage) { msg.PasswordCiphertext = "" },
			wantErr: "password sync message passwordCiphertext is required",
		},
		{
			name:    "nonce",
			mutate:  func(msg *migration.PasswordSyncMessage) { msg.PasswordNonce = "" },
			wantErr: "password sync message passwordNonce is required",
		},
		{
			name:    "key id",
			mutate:  func(msg *migration.PasswordSyncMessage) { msg.PasswordKeyID = "" },
			wantErr: "password sync message passwordKeyId is required",
		},
		{
			name:    "algorithm",
			mutate:  func(msg *migration.PasswordSyncMessage) { msg.PasswordAlg = "" },
			wantErr: "password sync message passwordAlg is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := validPasswordSyncMessage()
			tt.mutate(&msg)

			_, err := decodePasswordSyncMessage(workerMessage(t, msg))
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("decodePasswordSyncMessage error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestWorkerDecryptFailureRecordsSafeDLQWithoutCiphertext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onComplete = cancel
	deadLetters := &fakeDeadLetterSink{onRecord: cancel}
	worker := newPolicyTestWorker(t, receiver, &fakeProcessor{}, &fakePasswordDecrypter{err: errors.New("decrypt password payload failed")}, deadLetters, &fakeSleeper{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	entry := deadLetters.entries[0]
	if entry.Reason != DeadLetterReasonPermanentProcessor {
		t.Fatalf("safe DLQ reason = %q, want %q", entry.Reason, DeadLetterReasonPermanentProcessor)
	}
	assertSafeDLQEntry(t, entry)
	if receiver.completed != 1 || receiver.abandoned != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}
}

func TestWorkerContextCancellationSkipsRemainingMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := workerMessage(t, validPasswordSyncMessage())
	second := workerMessage(t, validPasswordSyncMessage())
	receiver := &fakeReceiver{messages: []*Message{first, second}}
	processor := &fakeProcessor{afterCall: cancel}
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, &fakeDeadLetterSink{})

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
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, &fakeDeadLetterSink{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if receiver.completed != 1 {
		t.Fatalf("completed = %d, want 1", receiver.completed)
	}
	if receiver.abandoned != 0 {
		t.Fatalf("abandoned = %d, want 0", receiver.abandoned)
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
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, &fakeDeadLetterSink{})

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

func TestWorkerEmptyReceiveWaitsBeforePollingAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &emptyBatchReceiver{firstCall: make(chan struct{})}
	processor := &fakeProcessor{}
	worker, err := New(receiver, processor, Options{
		MaxMessages:       1,
		EmptyReceiveDelay: 50 * time.Millisecond,
		DeadLetterSink:    &fakeDeadLetterSink{},
		PasswordDecrypter: &fakePasswordDecrypter{},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-receiver.firstCall:
	case <-time.After(time.Second):
		t.Fatal("worker did not call ReceiveMessages")
	}

	time.Sleep(10 * time.Millisecond)
	if calls := receiver.calls.Load(); calls != 1 {
		t.Fatalf("ReceiveMessages calls before empty delay elapsed = %d, want 1", calls)
	}
	if processor.calls != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls)
	}
	if receiver.completed != 0 || receiver.abandoned != 0 {
		t.Fatalf("unexpected settlements: completed=%d abandoned=%d", receiver.completed, receiver.abandoned)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestNewValidatesDependencies(t *testing.T) {
	processor := &fakeProcessor{}
	receiver := &fakeReceiver{}

	if _, err := New(nil, processor, Options{DeadLetterSink: &fakeDeadLetterSink{}, PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker receiver is required" {
		t.Fatalf("New with nil receiver error = %v", err)
	}
	if _, err := New(receiver, nil, Options{DeadLetterSink: &fakeDeadLetterSink{}, PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker processor is required" {
		t.Fatalf("New with nil processor error = %v", err)
	}
	if _, err := New(receiver, processor, Options{PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker dead-letter sink is required" {
		t.Fatalf("New without DLQ sink error = %v", err)
	}
	if _, err := New(receiver, processor, Options{DeadLetterSink: &fakeDeadLetterSink{}}); err == nil || err.Error() != "worker password decrypter is required" {
		t.Fatalf("New without decrypter error = %v", err)
	}
}

func newTestWorker(t *testing.T, receiver Receiver, processor Processor, decrypter PasswordDecrypter, deadLetters DeadLetterSink) *Worker {
	t.Helper()

	worker, err := New(receiver, processor, Options{
		MaxMessages:       10,
		DeadLetterSink:    deadLetters,
		PasswordDecrypter: decrypter,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return worker
}

func newPolicyTestWorker(t *testing.T, receiver Receiver, processor Processor, decrypter PasswordDecrypter, deadLetters *fakeDeadLetterSink, sleeper *fakeSleeper) *Worker {
	t.Helper()

	worker, err := New(receiver, processor, Options{
		MaxMessages:       10,
		DeadLetterSink:    deadLetters,
		PasswordDecrypter: decrypter,
		Sleep:             sleeper.Sleep,
		Now:               func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
	})
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
		CN:                 "u1234567",
		UPN:                "u1234567@example.edu",
		PasswordCiphertext: "ciphertext",
		PasswordNonce:      "nonce",
		PasswordKeyID:      "password-payload-key-v1",
		PasswordAlg:        passwordcrypto.AlgorithmAES256GCM,
		DisplayName:        "Test User",
		Mail:               "test@example.edu",
		EnqueuedAt:         time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC),
	}
}

func assertSafeDLQEntry(t *testing.T, entry DeadLetterEntry) {
	t.Helper()

	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal DeadLetterEntry returned error: %v", err)
	}
	for _, forbidden := range []string{`"password"`, "cleartext-password", "super-secret-password", "passwordCiphertext", "ciphertext", "passwordNonce", "nonce"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("safe DLQ entry leaked %q: %s", forbidden, body)
		}
	}
}

type fakeReceiver struct {
	messages []*Message

	completed int
	abandoned int

	completeErr error
	abandonErr  error

	onComplete func()
	onAbandon  func()

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

type emptyBatchReceiver struct {
	calls     atomic.Int32
	firstCall chan struct{}

	completed int
	abandoned int
}

func (r *emptyBatchReceiver) ReceiveMessages(ctx context.Context, maxMessages int) ([]*Message, error) {
	if r.calls.Add(1) == 1 {
		close(r.firstCall)
	}
	return nil, nil
}

func (r *emptyBatchReceiver) CompleteMessage(ctx context.Context, msg *Message) error {
	r.completed++
	return nil
}

func (r *emptyBatchReceiver) AbandonMessage(ctx context.Context, msg *Message) error {
	r.abandoned++
	return nil
}

type fakePasswordDecrypter struct {
	plaintext          []byte
	err                error
	calls              int
	returnedPlaintexts [][]byte
}

func (d *fakePasswordDecrypter) Decrypt(ctx context.Context, env passwordcrypto.Envelope, aad []byte) ([]byte, error) {
	d.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.err != nil {
		return nil, d.err
	}
	plaintext := append([]byte(nil), d.plaintext...)
	d.returnedPlaintexts = append(d.returnedPlaintexts, plaintext)
	return plaintext, nil
}

type fakeProcessor struct {
	calls     int
	messages  []migration.PasswordSyncMessage
	err       error
	errs      []error
	afterCall func()
}

func (p *fakeProcessor) ProcessPasswordSync(ctx context.Context, msg migration.PasswordSyncMessage) error {
	p.calls++
	p.messages = append(p.messages, msg)
	if p.afterCall != nil {
		p.afterCall()
	}
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return err
	}
	return p.err
}

type fakeDeadLetterSink struct {
	entries  []DeadLetterEntry
	err      error
	onRecord func()
}

func (s *fakeDeadLetterSink) RecordPasswordSyncFailure(ctx context.Context, entry DeadLetterEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	if s.onRecord != nil {
		s.onRecord()
	}
	return s.err
}

type fakeSleeper struct {
	durations []time.Duration
	err       error
	onSleep   func()
}

func (s *fakeSleeper) Sleep(ctx context.Context, delay time.Duration) error {
	if s.onSleep != nil {
		s.onSleep()
	}
	s.durations = append(s.durations, delay)
	return s.err
}
