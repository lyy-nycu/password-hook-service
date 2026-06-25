package servicebusqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
)

const defaultTTL = 300 * time.Second

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
	var senderErr error
	if q.sender != nil {
		senderErr = q.sender.Close(ctx)
	}
	var clientErr error
	if q.client != nil {
		clientErr = q.client.Close(ctx)
	}
	if senderErr != nil {
		return fmt.Errorf("close service bus sender: %w", senderErr)
	}
	if clientErr != nil {
		return fmt.Errorf("close service bus client: %w", clientErr)
	}
	return nil
}
