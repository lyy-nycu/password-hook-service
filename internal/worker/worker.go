package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nycu/password-hook-service/internal/migration"
)

const (
	passwordSyncKind = "password-sync"

	deadLetterReasonInvalidMessageSchema = "invalid_message_schema"
	deadLetterReasonPermanentProcessor   = "permanent_processor_error"
	deadLetterDescriptionInvalidMessage  = "invalid password sync message"
	deadLetterDescriptionPermanentError  = "permanent processor error"
	defaultSettlementTimeout             = 10 * time.Second
	defaultEmptyReceiveDelay             = 250 * time.Millisecond
)

type Message struct {
	Body []byte
	Kind string
}

type Receiver interface {
	ReceiveMessages(context.Context, int) ([]*Message, error)
	CompleteMessage(context.Context, *Message) error
	AbandonMessage(context.Context, *Message) error
	DeadLetterMessage(context.Context, *Message, string, string) error
}

type Processor interface {
	ProcessPasswordSync(context.Context, migration.PasswordSyncMessage) error
}

type Options struct {
	MaxMessages       int
	SettlementTimeout time.Duration
	EmptyReceiveDelay time.Duration
}

type PermanentReason string

const PermanentReasonProcessorError PermanentReason = deadLetterReasonPermanentProcessor

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
	receiver          Receiver
	processor         Processor
	maxMessages       int
	settlementTimeout time.Duration
	emptyReceiveDelay time.Duration
}

func New(receiver Receiver, processor Processor, options Options) (*Worker, error) {
	if receiver == nil {
		return nil, errors.New("worker receiver is required")
	}
	if processor == nil {
		return nil, errors.New("worker processor is required")
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
	return &Worker{
		receiver:          receiver,
		processor:         processor,
		maxMessages:       options.MaxMessages,
		settlementTimeout: options.SettlementTimeout,
		emptyReceiveDelay: options.EmptyReceiveDelay,
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
	timer := time.NewTimer(w.emptyReceiveDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (w *Worker) processMessage(ctx context.Context, msg *Message) error {
	passwordSyncMessage, err := decodePasswordSyncMessage(msg)
	if err != nil {
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.DeadLetterMessage(settleCtx, msg, deadLetterReasonInvalidMessageSchema, deadLetterDescriptionInvalidMessage); settleErr != nil {
			return fmt.Errorf("dead-letter invalid worker message: %w", settleErr)
		}
		return nil
	}

	err = w.processor.ProcessPasswordSync(ctx, passwordSyncMessage)
	if err == nil {
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.CompleteMessage(settleCtx, msg); settleErr != nil {
			return fmt.Errorf("complete worker message: %w", settleErr)
		}
		return nil
	}

	var permanentErr *PermanentError
	if errors.As(err, &permanentErr) {
		reason := permanentDeadLetterReason(permanentErr)
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.DeadLetterMessage(settleCtx, msg, reason, deadLetterDescriptionPermanentError); settleErr != nil {
			return fmt.Errorf("dead-letter permanent worker message: %w", settleErr)
		}
		return nil
	}

	settleCtx, cancel := w.settlementContext()
	defer cancel()
	if settleErr := w.receiver.AbandonMessage(settleCtx, msg); settleErr != nil {
		return fmt.Errorf("abandon worker message: %w", settleErr)
	}
	return nil
}

func (w *Worker) settlementContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), w.settlementTimeout)
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
	if out.Password == "" {
		return migration.PasswordSyncMessage{}, errors.New("password sync message password is required")
	}
	if out.EnqueuedAt.IsZero() {
		return migration.PasswordSyncMessage{}, errors.New("password sync message EnqueuedAt is required")
	}
	return out, nil
}

func permanentDeadLetterReason(err *PermanentError) string {
	if err == nil {
		return deadLetterReasonPermanentProcessor
	}
	switch err.Reason {
	case PermanentReasonProcessorError:
		return string(err.Reason)
	default:
		return deadLetterReasonPermanentProcessor
	}
}
