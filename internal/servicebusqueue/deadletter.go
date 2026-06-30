package servicebusqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/worker"
)

const passwordSyncDLQKind = "password-sync-dlq"

type DeadLetterQueue struct {
	sender sender
	client closer
}

var _ worker.DeadLetterSink = (*DeadLetterQueue)(nil)

func NewDeadLetterQueue(sender sender) (*DeadLetterQueue, error) {
	return NewDeadLetterQueueWithClient(sender, nil)
}

func NewDeadLetterQueueWithClient(sender sender, client closer) (*DeadLetterQueue, error) {
	if sender == nil {
		return nil, errors.New("service bus dead-letter sender is required")
	}
	return &DeadLetterQueue{
		sender: sender,
		client: client,
	}, nil
}

func NewDeadLetterQueueFromConnectionString(connectionString string, queueName string) (*DeadLetterQueue, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}

	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create service bus dead-letter sender: %w", err),
			closeWithTimeout(context.Background(), client),
		)
	}

	return NewDeadLetterQueueWithClient(sender, client)
}

func (q *DeadLetterQueue) RecordPasswordSyncFailure(ctx context.Context, entry worker.DeadLetterEntry) error {
	entry.Password = ""
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal password sync dead-letter message: %w", err)
	}

	contentType := "application/json"
	subject := passwordSyncDLQKind
	message := &azservicebus.Message{
		ApplicationProperties: map[string]any{
			"kind":   passwordSyncDLQKind,
			"cn":     entry.CN,
			"upn":    entry.UPN,
			"reason": entry.Reason,
		},
		Body:        body,
		ContentType: &contentType,
		Subject:     &subject,
	}

	if err := q.sender.SendMessage(ctx, message, nil); err != nil {
		return fmt.Errorf("send password sync dead-letter message: %w", err)
	}
	return nil
}

func (q *DeadLetterQueue) Close(ctx context.Context) error {
	var closeErrs []error
	if q.sender != nil {
		if err := q.sender.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus dead-letter sender: %w", err))
		}
	}
	if q.client != nil {
		if err := q.client.Close(ctx); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close service bus client: %w", err))
		}
	}
	return errors.Join(closeErrs...)
}
