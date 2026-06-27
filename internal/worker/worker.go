package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nycu/password-hook-service/internal/migration"
)

const (
	passwordSyncKind = "password-sync"

	deadLetterReasonInvalidMessageSchema = "invalid_message_schema"
	deadLetterDescriptionInvalidMessage  = "invalid password sync message"
	deadLetterDescriptionPermanentError  = "permanent processor error"
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
	MaxMessages int
}

type PermanentError struct {
	Reason string
	Err    error
}

func (e *PermanentError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Reason
	}
	if strings.TrimSpace(e.Reason) == "" {
		return e.Err.Error()
	}
	return e.Reason + ": " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Worker struct {
	receiver    Receiver
	processor   Processor
	maxMessages int
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
	return &Worker{
		receiver:    receiver,
		processor:   processor,
		maxMessages: options.MaxMessages,
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

func (w *Worker) processMessage(ctx context.Context, msg *Message) error {
	passwordSyncMessage, err := decodePasswordSyncMessage(msg)
	if err != nil {
		if settleErr := w.receiver.DeadLetterMessage(ctx, msg, deadLetterReasonInvalidMessageSchema, deadLetterDescriptionInvalidMessage); settleErr != nil {
			return fmt.Errorf("dead-letter invalid worker message: %w", settleErr)
		}
		return nil
	}

	err = w.processor.ProcessPasswordSync(ctx, passwordSyncMessage)
	if err == nil {
		if settleErr := w.receiver.CompleteMessage(ctx, msg); settleErr != nil {
			return fmt.Errorf("complete worker message: %w", settleErr)
		}
		return nil
	}

	var permanentErr *PermanentError
	if errors.As(err, &permanentErr) {
		reason := sanitizeDeadLetterReason(permanentErr.Reason, "permanent_processor_error")
		if settleErr := w.receiver.DeadLetterMessage(ctx, msg, reason, deadLetterDescriptionPermanentError); settleErr != nil {
			return fmt.Errorf("dead-letter permanent worker message: %w", settleErr)
		}
		return nil
	}

	if settleErr := w.receiver.AbandonMessage(ctx, msg); settleErr != nil {
		return fmt.Errorf("abandon worker message: %w", settleErr)
	}
	return nil
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

func sanitizeDeadLetterReason(reason string, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fallback
	}

	var b strings.Builder
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 128 {
			break
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}
