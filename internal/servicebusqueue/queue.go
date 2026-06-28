package servicebusqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
)

const (
	defaultTTL   = 300 * time.Second
	closeTimeout = 5 * time.Second
)

type sender interface {
	SendMessage(context.Context, *azservicebus.Message, *azservicebus.SendMessageOptions) error
	Close(context.Context) error
}

type closer interface {
	Close(context.Context) error
}

type Queue struct {
	sender sender
	client closer
	ttl    time.Duration
}

var _ migration.Queue = (*Queue)(nil)

func New(sender sender, ttl time.Duration) (*Queue, error) {
	return NewWithClient(sender, nil, ttl)
}

func NewWithClient(sender sender, client closer, ttl time.Duration) (*Queue, error) {
	if sender == nil {
		return nil, errors.New("service bus sender is required")
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Queue{
		sender: sender,
		client: client,
		ttl:    ttl,
	}, nil
}

func NewFromConnectionString(connectionString string, queueName string, ttl time.Duration) (*Queue, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}

	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create service bus sender: %w", err),
			closeWithTimeout(context.Background(), client),
		)
	}

	return NewWithClient(sender, client, ttl)
}

func closeWithTimeout(ctx context.Context, closer closer) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	return closer.Close(closeCtx)
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
