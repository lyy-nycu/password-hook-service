package servicebusqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/worker"
)

const defaultTTL = 300 * time.Second

type sender interface {
	SendMessage(context.Context, *azservicebus.Message, *azservicebus.SendMessageOptions) error
	Close(context.Context) error
}

type closer interface {
	Close(context.Context) error
}

type serviceBusReceiver interface {
	ReceiveMessages(context.Context, int, *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	CompleteMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.CompleteMessageOptions) error
	AbandonMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.AbandonMessageOptions) error
	DeadLetterMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeadLetterOptions) error
	Close(context.Context) error
}

type Queue struct {
	sender sender
	client closer
	ttl    time.Duration
}

var _ migration.Queue = (*Queue)(nil)

type Receiver struct {
	receiver serviceBusReceiver
	client   closer
	native   map[*worker.Message]*azservicebus.ReceivedMessage
}

var _ worker.Receiver = (*Receiver)(nil)

func New(sender sender, ttl time.Duration) *Queue {
	return NewWithClient(sender, nil, ttl)
}

func NewWithClient(sender sender, client closer, ttl time.Duration) *Queue {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Queue{
		sender: sender,
		client: client,
		ttl:    ttl,
	}
}

func NewFromConnectionString(connectionString string, queueName string, ttl time.Duration) (*Queue, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}

	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, fmt.Errorf("create service bus sender: %w", err)
	}

	return NewWithClient(sender, client, ttl), nil
}

func NewReceiver(receiver serviceBusReceiver) *Receiver {
	return NewReceiverWithClient(receiver, nil)
}

func NewReceiverWithClient(receiver serviceBusReceiver, client closer) *Receiver {
	return &Receiver{
		receiver: receiver,
		client:   client,
		native:   make(map[*worker.Message]*azservicebus.ReceivedMessage),
	}
}

func NewReceiverFromConnectionString(connectionString string, queueName string) (*Receiver, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}

	receiver, err := client.NewReceiverForQueue(queueName, &azservicebus.ReceiverOptions{
		ReceiveMode: azservicebus.ReceiveModePeekLock,
	})
	if err != nil {
		_ = client.Close(context.Background())
		return nil, fmt.Errorf("create service bus receiver: %w", err)
	}

	return NewReceiverWithClient(receiver, client), nil
}

func (q *Queue) EnqueuePasswordSync(ctx context.Context, msg migration.PasswordSyncMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal password sync message: %w", err)
	}

	contentType := "application/json"
	subject := "password-sync"
	messageID := fmt.Sprintf("%s:%s", msg.UPN, msg.EnqueuedAt.UTC().Format(time.RFC3339Nano))
	serviceBusMessage := &azservicebus.Message{
		ApplicationProperties: map[string]any{
			"kind": "password-sync",
			"cn":   msg.CN,
			"upn":  msg.UPN,
		},
		Body:        body,
		ContentType: &contentType,
		MessageID:   &messageID,
		Subject:     &subject,
		TimeToLive:  &q.ttl,
	}

	if err := q.sender.SendMessage(ctx, serviceBusMessage, nil); err != nil {
		return fmt.Errorf("send password sync message: %w", err)
	}
	return nil
}

func (q *Queue) Close(ctx context.Context) error {
	var closeErrs []error
	if q.sender != nil {
		if err := q.sender.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus sender: %w", err))
		}
	}
	if q.client != nil {
		if err := q.client.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus client: %w", err))
		}
	}
	return errors.Join(closeErrs...)
}

func (r *Receiver) ReceiveMessages(ctx context.Context, maxMessages int) ([]*worker.Message, error) {
	messages, err := r.receiver.ReceiveMessages(ctx, maxMessages, nil)
	if err != nil {
		return nil, err
	}

	out := make([]*worker.Message, 0, len(messages))
	for _, msg := range messages {
		workerMessage := &worker.Message{
			Body: append([]byte(nil), msg.Body...),
			Kind: messageKind(msg),
		}
		r.native[workerMessage] = msg
		out = append(out, workerMessage)
	}
	return out, nil
}

func (r *Receiver) CompleteMessage(ctx context.Context, msg *worker.Message) error {
	native, err := r.nativeMessage(msg)
	if err != nil {
		return err
	}
	if err := r.receiver.CompleteMessage(ctx, native, nil); err != nil {
		return err
	}
	delete(r.native, msg)
	return nil
}

func (r *Receiver) AbandonMessage(ctx context.Context, msg *worker.Message) error {
	native, err := r.nativeMessage(msg)
	if err != nil {
		return err
	}
	if err := r.receiver.AbandonMessage(ctx, native, nil); err != nil {
		return err
	}
	delete(r.native, msg)
	return nil
}

func (r *Receiver) DeadLetterMessage(ctx context.Context, msg *worker.Message, reason string, description string) error {
	native, err := r.nativeMessage(msg)
	if err != nil {
		return err
	}
	if err := r.receiver.DeadLetterMessage(ctx, native, &azservicebus.DeadLetterOptions{
		Reason:           &reason,
		ErrorDescription: &description,
	}); err != nil {
		return err
	}
	delete(r.native, msg)
	return nil
}

func (r *Receiver) Close(ctx context.Context) error {
	var closeErrs []error
	if r.receiver != nil {
		if err := r.receiver.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus receiver: %w", err))
		}
	}
	if r.client != nil {
		if err := r.client.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus client: %w", err))
		}
	}
	return errors.Join(closeErrs...)
}

func (r *Receiver) nativeMessage(msg *worker.Message) (*azservicebus.ReceivedMessage, error) {
	native, ok := r.native[msg]
	if !ok {
		return nil, errors.New("worker message was not received by this service bus receiver")
	}
	return native, nil
}

func messageKind(msg *azservicebus.ReceivedMessage) string {
	if msg == nil {
		return ""
	}
	if value, ok := msg.ApplicationProperties["kind"].(string); ok {
		return value
	}
	if msg.Subject != nil {
		return *msg.Subject
	}
	return ""
}
