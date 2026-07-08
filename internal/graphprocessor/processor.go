package graphprocessor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/worker"
)

type Processor struct {
	client   graphclient.Client
	logger   *slog.Logger
	recorder observability.Recorder
	now      func() time.Time
}

type Options struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
	Now      func() time.Time
}

func New(client graphclient.Client) (*Processor, error) {
	return NewWithOptions(client, Options{})
}

func NewWithOptions(client graphclient.Client, options Options) (*Processor, error) {
	if client == nil {
		return nil, errors.New("graph client is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Processor{client: client, logger: options.Logger, recorder: options.Recorder, now: options.Now}, nil
}

func (p *Processor) ProcessPasswordSync(ctx context.Context, msg worker.PasswordSyncCommand) error {
	start := p.now()
	err := p.client.UpsertUserPassword(ctx, graphclient.User{
		UPN:         msg.UPN,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
	}, msg.Password)

	outcome := "success"
	if err != nil {
		outcome = "transient_error"
		var permanent *graphclient.PermanentError
		if errors.As(err, &permanent) {
			outcome = "permanent_error"
		}
	}
	duration := p.now().Sub(start)
	p.recorder.ObserveDuration(ctx, observability.MetricGraphUpsertDuration, duration, observability.Labels{"outcome": outcome})
	attrs := []slog.Attr{
		slog.String("action", observability.ActionGraphUpsert),
		slog.String("outcome", outcome),
		slog.Int64("durationMs", duration.Milliseconds()),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID: msg.TraceID,
		UPN:     msg.UPN,
	})...)
	p.logger.LogAttrs(ctx, slog.LevelInfo, observability.ActionGraphUpsert, attrs...)

	if err == nil {
		return nil
	}
	var permanent *graphclient.PermanentError
	if errors.As(err, &permanent) {
		return &worker.PermanentError{Reason: worker.PermanentReasonProcessorError, Err: permanent}
	}
	return err
}
