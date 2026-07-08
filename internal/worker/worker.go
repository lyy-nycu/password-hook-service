package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

const (
	passwordSyncKind = "password-sync"

	DeadLetterReasonInvalidMessageSchema      = "invalid_message_schema"
	DeadLetterReasonPermanentProcessor        = "permanent_processor_error"
	DeadLetterReasonTransientRetriesExhausted = "transient_processor_retries_exhausted"

	dlqReasonInvalidMessageSchema  = DeadLetterReasonInvalidMessageSchema
	dlqReasonPermanentProcessor    = DeadLetterReasonPermanentProcessor
	dlqDescriptionInvalidMessage   = "invalid password sync message"
	dlqDescriptionPermanentError   = "permanent processor error"
	dlqDescriptionRetriesExhausted = "transient processor retries exhausted"
	defaultSettlementTimeout       = 10 * time.Second
	defaultEmptyReceiveDelay       = 250 * time.Millisecond
)

var defaultRetryBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

type Message struct {
	Body []byte
	Kind string
}

type Receiver interface {
	ReceiveMessages(context.Context, int) ([]*Message, error)
	CompleteMessage(context.Context, *Message) error
	AbandonMessage(context.Context, *Message) error
}

// PasswordSyncCommand is the processor handoff for a decrypted password sync.
// Password is a borrowed mutable buffer that is valid only during the
// ProcessPasswordSync call. Processor implementations must not retain it, use
// it asynchronously, or assume it remains readable after ProcessPasswordSync
// returns.
type PasswordSyncCommand struct {
	CN          string
	UPN         string
	TraceID     string
	Password    []byte
	DisplayName string
	Mail        string
	EnqueuedAt  time.Time
}

type Processor interface {
	// ProcessPasswordSync must treat msg.Password as call-scoped borrowed
	// memory. The worker zeroes it immediately after this method returns.
	ProcessPasswordSync(context.Context, PasswordSyncCommand) error
}

type PasswordDecrypter interface {
	Decrypt(context.Context, passwordcrypto.Envelope, []byte) ([]byte, error)
}

type DeadLetterEntry struct {
	Kind        string    `json:"kind"`
	CN          string    `json:"cn,omitempty"`
	UPN         string    `json:"upn,omitempty"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	Attempts    int       `json:"attempts"`
	EnqueuedAt  time.Time `json:"enqueuedAt,omitempty"`
	FailedAt    time.Time `json:"failedAt"`

	Password string `json:"-"`
}

type DeadLetterSink interface {
	RecordPasswordSyncFailure(context.Context, DeadLetterEntry) error
}

// SyncStatusRecorder records the outcome of processing a password sync
// message, so migration.Service can dedupe future login_bootstrap events for
// the same UPN. Implementations use sourceEnqueuedAt to ignore stale,
// out-of-order completions.
type SyncStatusRecorder interface {
	MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
	MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
}

type Options struct {
	MaxMessages        int
	SettlementTimeout  time.Duration
	EmptyReceiveDelay  time.Duration
	RetryBackoffs      []time.Duration
	DeadLetterSink     DeadLetterSink
	PasswordDecrypter  PasswordDecrypter
	SyncStatusRecorder SyncStatusRecorder
	Logger             *slog.Logger
	Recorder           observability.Recorder
	Now                func() time.Time
	Sleep              func(context.Context, time.Duration) error
}

type PermanentReason string

const PermanentReasonProcessorError PermanentReason = dlqReasonPermanentProcessor

type PermanentError struct {
	Reason PermanentReason
	Err    error
}

func (e *PermanentError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Reason)
	}
	if strings.TrimSpace(string(e.Reason)) == "" {
		return e.Err.Error()
	}
	return string(e.Reason) + ": " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Worker struct {
	receiver           Receiver
	processor          Processor
	passwordDecrypter  PasswordDecrypter
	syncStatusRecorder SyncStatusRecorder
	maxMessages        int
	settlementTimeout  time.Duration
	emptyReceiveDelay  time.Duration
	retryBackoffs      []time.Duration
	deadLetterSink     DeadLetterSink
	logger             *slog.Logger
	recorder           observability.Recorder
	now                func() time.Time
	sleep              func(context.Context, time.Duration) error
}

func New(receiver Receiver, processor Processor, options Options) (*Worker, error) {
	if receiver == nil {
		return nil, errors.New("worker receiver is required")
	}
	if processor == nil {
		return nil, errors.New("worker processor is required")
	}
	if options.DeadLetterSink == nil {
		return nil, errors.New("worker dead-letter sink is required")
	}
	if options.PasswordDecrypter == nil {
		return nil, errors.New("worker password decrypter is required")
	}
	if options.SyncStatusRecorder == nil {
		return nil, errors.New("worker sync status recorder is required")
	}
	if options.MaxMessages <= 0 {
		options.MaxMessages = 1
	}
	if options.SettlementTimeout <= 0 {
		options.SettlementTimeout = defaultSettlementTimeout
	}
	if options.EmptyReceiveDelay <= 0 {
		options.EmptyReceiveDelay = defaultEmptyReceiveDelay
	}
	if len(options.RetryBackoffs) == 0 {
		options.RetryBackoffs = defaultRetryBackoffs
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sleep == nil {
		options.Sleep = sleep
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return &Worker{
		receiver:           receiver,
		processor:          processor,
		passwordDecrypter:  options.PasswordDecrypter,
		syncStatusRecorder: options.SyncStatusRecorder,
		maxMessages:        options.MaxMessages,
		settlementTimeout:  options.SettlementTimeout,
		emptyReceiveDelay:  options.EmptyReceiveDelay,
		retryBackoffs:      append([]time.Duration(nil), options.RetryBackoffs...),
		deadLetterSink:     options.DeadLetterSink,
		logger:             options.Logger,
		recorder:           options.Recorder,
		now:                options.Now,
		sleep:              options.Sleep,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		messages, err := w.receiver.ReceiveMessages(ctx, w.maxMessages)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("receive worker messages: %w", err)
		}
		if len(messages) == 0 {
			if err := w.waitAfterEmptyReceive(ctx); err != nil {
				return nil
			}
			continue
		}

		for _, msg := range messages {
			if err := ctx.Err(); err != nil {
				return nil
			}
			if err := w.processMessage(ctx, msg); err != nil {
				return err
			}
		}
	}
}

func (w *Worker) waitAfterEmptyReceive(ctx context.Context) error {
	return w.sleep(ctx, w.emptyReceiveDelay)
}

func (w *Worker) processMessage(ctx context.Context, msg *Message) error {
	passwordSyncMessage, err := decodePasswordSyncMessage(msg)
	if err != nil {
		entry := invalidMessageDeadLetterEntry(msg, w.now())
		zeroMessageBody(msg)
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		observedMessage := invalidMessageForObservability(entry)
		if settleErr := w.recordPasswordSyncFailure(settleCtx, entry); settleErr != nil {
			return w.abandonAfterDeadLetterFailure(settleCtx, msg, "record invalid worker message dead-letter", settleErr, observedMessage, 0)
		}
		if settleErr := w.receiver.CompleteMessage(settleCtx, msg); settleErr != nil {
			return fmt.Errorf("complete invalid worker message: %w", settleErr)
		}
		w.recordOutcome(ctx, observability.ActionWorkerInvalid, observedMessage, "invalid_message", DeadLetterReasonInvalidMessageSchema, 0)
		return nil
	}

	result := w.processPasswordSync(ctx, passwordSyncMessage)
	if result.err == nil {
		zeroMessageBody(msg)
		_ = w.syncStatusRecorder.MarkSynced(ctx, passwordSyncMessage.UPN, passwordSyncMessage.EnqueuedAt)
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.CompleteMessage(settleCtx, msg); settleErr != nil {
			return fmt.Errorf("complete worker message: %w", settleErr)
		}
		w.recordOutcome(ctx, observability.ActionWorkerCompleted, passwordSyncMessage, "synced", "", result.attempts)
		return nil
	}

	if result.retryCanceled {
		zeroMessageBody(msg)
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.AbandonMessage(settleCtx, msg); settleErr != nil {
			return fmt.Errorf("abandon worker message: %w", settleErr)
		}
		w.recordOutcome(ctx, observability.ActionWorkerAbandoned, passwordSyncMessage, "abandoned", "", result.attempts)
		return nil
	}

	reason := DeadLetterReasonTransientRetriesExhausted
	description := dlqDescriptionRetriesExhausted
	if result.permanent {
		reason = DeadLetterReasonPermanentProcessor
		description = dlqDescriptionPermanentError
	}

	zeroMessageBody(msg)
	settleCtx, cancel := w.settlementContext()
	defer cancel()
	if settleErr := w.recordPasswordSyncFailure(settleCtx, DeadLetterEntry{
		Kind:        passwordSyncKind,
		CN:          passwordSyncMessage.CN,
		UPN:         passwordSyncMessage.UPN,
		Reason:      reason,
		Description: description,
		Attempts:    result.attempts,
		EnqueuedAt:  passwordSyncMessage.EnqueuedAt,
		FailedAt:    w.now(),
	}); settleErr != nil {
		return w.abandonAfterDeadLetterFailure(settleCtx, msg, "record worker message dead-letter", settleErr, passwordSyncMessage, result.attempts)
	}
	_ = w.syncStatusRecorder.MarkFailed(ctx, passwordSyncMessage.UPN, passwordSyncMessage.EnqueuedAt)
	if settleErr := w.receiver.CompleteMessage(settleCtx, msg); settleErr != nil {
		return fmt.Errorf("complete failed worker message: %w", settleErr)
	}
	w.recordOutcome(ctx, observability.ActionWorkerFailed, passwordSyncMessage, "sync_failed", reason, result.attempts)
	return nil
}

func (w *Worker) recordOutcome(ctx context.Context, action string, msg migration.PasswordSyncMessage, outcome string, reason string, attempts int) {
	labels := observability.Labels{
		"outcome": outcome,
	}
	if msg.EventType != "" {
		labels["eventType"] = string(msg.EventType)
	}
	if reason != "" {
		labels["reason"] = reason
	}
	if attempts > 0 {
		labels["attempts"] = fmt.Sprint(attempts)
	}
	w.recorder.Inc(ctx, observability.MetricWorkerMessagesTotal, labels)
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("outcome", outcome),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID:   msg.TraceID,
		CN:        msg.CN,
		UPN:       msg.UPN,
		EventType: string(msg.EventType),
	})...)
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	if attempts > 0 {
		attrs = append(attrs, slog.Int("attempts", attempts))
	}
	w.logger.LogAttrs(ctx, slog.LevelInfo, action, attrs...)
}

func invalidMessageForObservability(entry DeadLetterEntry) migration.PasswordSyncMessage {
	return migration.PasswordSyncMessage{CN: entry.CN, UPN: entry.UPN, EnqueuedAt: entry.EnqueuedAt}
}

type processorResult struct {
	err           error
	attempts      int
	permanent     bool
	retryCanceled bool
}

func (w *Worker) processPasswordSync(ctx context.Context, msg migration.PasswordSyncMessage) processorResult {
	for attempts := 1; ; attempts++ {
		plaintext, err := w.passwordDecrypter.Decrypt(ctx, passwordcrypto.Envelope{
			Ciphertext: msg.PasswordCiphertext,
			Nonce:      msg.PasswordNonce,
			KeyID:      msg.PasswordKeyID,
			Algorithm:  msg.PasswordAlg,
		}, migration.PasswordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
		if err != nil {
			return processorResult{err: &PermanentError{Reason: PermanentReasonProcessorError, Err: err}, attempts: attempts, permanent: true}
		}

		err = w.processPasswordSyncAttempt(ctx, msg, plaintext)
		if err == nil {
			return processorResult{attempts: attempts}
		}

		var permanentErr *PermanentError
		if errors.As(err, &permanentErr) {
			return processorResult{err: permanentErr, attempts: attempts, permanent: true}
		}

		if attempts > len(w.retryBackoffs) {
			return processorResult{err: err, attempts: attempts}
		}
		if sleepErr := w.sleep(ctx, w.retryBackoffs[attempts-1]); sleepErr != nil {
			return processorResult{err: sleepErr, attempts: attempts, retryCanceled: true}
		}
	}
}

func (w *Worker) processPasswordSyncAttempt(ctx context.Context, msg migration.PasswordSyncMessage, plaintext []byte) error {
	defer passwordcrypto.ZeroBytes(plaintext)
	return w.processor.ProcessPasswordSync(ctx, PasswordSyncCommand{
		CN:          msg.CN,
		UPN:         msg.UPN,
		TraceID:     msg.TraceID,
		Password:    plaintext,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
		EnqueuedAt:  msg.EnqueuedAt,
	})
}

func (w *Worker) settlementContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), w.settlementTimeout)
}

func (w *Worker) recordPasswordSyncFailure(ctx context.Context, entry DeadLetterEntry) error {
	entry.Password = ""
	return w.deadLetterSink.RecordPasswordSyncFailure(ctx, entry)
}

func (w *Worker) abandonAfterDeadLetterFailure(ctx context.Context, msg *Message, operation string, err error, observedMessage migration.PasswordSyncMessage, attempts int) error {
	recordErr := fmt.Errorf("%s: %w", operation, err)
	if abandonErr := w.receiver.AbandonMessage(ctx, msg); abandonErr != nil {
		return errors.Join(recordErr, fmt.Errorf("abandon worker message after dead-letter failure: %w", abandonErr))
	}
	w.recordOutcome(ctx, observability.ActionWorkerAbandoned, observedMessage, "abandoned", "safe_dlq_write_failed", attempts)
	return recordErr
}

func decodePasswordSyncMessage(msg *Message) (migration.PasswordSyncMessage, error) {
	if msg == nil {
		return migration.PasswordSyncMessage{}, errors.New("message is nil")
	}
	if strings.TrimSpace(msg.Kind) != passwordSyncKind {
		return migration.PasswordSyncMessage{}, fmt.Errorf("unexpected message kind %q", msg.Kind)
	}

	var out migration.PasswordSyncMessage
	if err := json.Unmarshal(msg.Body, &out); err != nil {
		return migration.PasswordSyncMessage{}, fmt.Errorf("decode password sync message: %w", err)
	}
	if strings.TrimSpace(out.CN) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message CN is required")
	}
	if strings.TrimSpace(out.UPN) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message UPN is required")
	}
	if strings.TrimSpace(out.PasswordCiphertext) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message passwordCiphertext is required")
	}
	if strings.TrimSpace(out.PasswordNonce) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message passwordNonce is required")
	}
	if strings.TrimSpace(out.PasswordKeyID) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message passwordKeyId is required")
	}
	if strings.TrimSpace(out.PasswordAlg) == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message passwordAlg is required")
	}
	if out.EnqueuedAt.IsZero() {
		return migration.PasswordSyncMessage{}, errors.New("password sync message EnqueuedAt is required")
	}
	return out, nil
}

func permanentDeadLetterReason(err *PermanentError) string {
	if err == nil {
		return dlqReasonPermanentProcessor
	}
	switch err.Reason {
	case PermanentReasonProcessorError:
		return string(err.Reason)
	default:
		return dlqReasonPermanentProcessor
	}
}

func invalidMessageDeadLetterEntry(msg *Message, failedAt time.Time) DeadLetterEntry {
	entry := DeadLetterEntry{
		Kind:        passwordSyncKind,
		Reason:      DeadLetterReasonInvalidMessageSchema,
		Description: dlqDescriptionInvalidMessage,
		Attempts:    0,
		FailedAt:    failedAt,
	}
	if msg == nil {
		return entry
	}
	if strings.TrimSpace(msg.Kind) != "" {
		entry.Kind = strings.TrimSpace(msg.Kind)
	}

	var partial struct {
		CN         string    `json:"cn"`
		UPN        string    `json:"upn"`
		EnqueuedAt time.Time `json:"enqueuedAt"`
	}
	if err := json.Unmarshal(msg.Body, &partial); err != nil {
		return entry
	}
	entry.CN = partial.CN
	entry.UPN = partial.UPN
	entry.EnqueuedAt = partial.EnqueuedAt
	return entry
}

func zeroMessageBody(msg *Message) {
	if msg == nil {
		return
	}
	passwordcrypto.ZeroBytes(msg.Body)
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
