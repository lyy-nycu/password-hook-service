package graphprocessor

import (
	"context"
	"errors"

	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/worker"
)

type Processor struct {
	client graphclient.Client
}

func New(client graphclient.Client) (*Processor, error) {
	if client == nil {
		return nil, errors.New("graph client is required")
	}
	return &Processor{client: client}, nil
}

func (p *Processor) ProcessPasswordSync(ctx context.Context, msg worker.PasswordSyncCommand) error {
	err := p.client.UpsertUserPassword(ctx, graphclient.User{
		UPN:         msg.UPN,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
	}, msg.Password)
	if err == nil {
		return nil
	}
	var permanent *graphclient.PermanentError
	if errors.As(err, &permanent) {
		return &worker.PermanentError{Reason: worker.PermanentReasonProcessorError, Err: permanent}
	}
	return err
}
