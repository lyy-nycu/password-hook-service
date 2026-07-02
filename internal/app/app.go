package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/graphprocessor"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/servicebusqueue"
	"github.com/nycu/password-hook-service/internal/worker"
)

const queueCloseTimeout = 5 * time.Second

type appWorker interface {
	Run(context.Context) error
}

type appCloser interface {
	Close(context.Context) error
}

type App struct {
	server  *httpserver.Server
	worker  appWorker
	closers []appCloser
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	queue, err := servicebusqueue.NewFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return nil, err
	}
	closers := []appCloser{queue}

	receiver, err := servicebusqueue.NewReceiverFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	closers = append(closers, receiver)

	dlq, err := servicebusqueue.NewDeadLetterQueueFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusDeadLetterQueueName)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	closers = append(closers, dlq)

	credential, err := azidentity.NewClientSecretCredential(cfg.GraphTenantID, cfg.GraphClientID, cfg.GraphClientSecret, nil)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	graph, err := graphclient.NewHTTPClient(credential, graphclient.Options{})
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	processor, err := graphprocessor.New(graph)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}

	return newWithWorkerDependencies(cfg, queue, receiver, processor, dlq, closers...)
}

func NewWithQueue(cfg config.Config, queue migration.Queue) (*App, error) {
	if err := cfg.ValidateHTTP(); err != nil {
		return nil, err
	}
	if err := validatePasswordEncryptionConfig(cfg); err != nil {
		return nil, err
	}
	if queue == nil {
		return nil, errors.New("migration queue is required")
	}
	return newWithQueue(cfg, queue)
}

func newWithWorkerDependencies(
	cfg config.Config,
	queue migration.Queue,
	receiver worker.Receiver,
	processor worker.Processor,
	deadLetterSink worker.DeadLetterSink,
	closers ...appCloser,
) (*App, error) {
	application, err := newWithQueue(cfg, queue, closers...)
	if err != nil {
		return nil, err
	}
	passwordCodec, err := passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	passwordWorker, err := worker.New(receiver, processor, worker.Options{
		DeadLetterSink:    deadLetterSink,
		PasswordDecrypter: passwordCodec,
	})
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	application.worker = passwordWorker
	return application, nil
}

func newWithQueue(cfg config.Config, queue migration.Queue, closers ...appCloser) (*App, error) {
	passwordCodec, err := passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	service := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordCodec)
	hook := handler.NewHook(service, cfg.ProblemBaseURL)
	hmacMiddleware, err := middleware.NewHMACWithProblemBase(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, cfg.ProblemBaseURL)
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	rateLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		AllowedCIDRs: cfg.PortalAllowedCIDRs,
		LimitPerIP:   cfg.RateLimitPerIP,
		Window:       cfg.RateLimitWindow,
		ProblemBase:  cfg.ProblemBaseURL,
	})

	hookHandler := hmacMiddleware.Wrap(hook)
	hookHandler = rateLimiter.Wrap(hookHandler)
	hookHandler = middleware.RecoveryWithProblemBase(slog.Default(), cfg.ProblemBaseURL)(hookHandler)
	hookHandler = middleware.AccessLog(slog.Default())(hookHandler)
	hookHandler = requestid.Middleware(hookHandler)

	server := httpserver.New(cfg.HTTPAddr, httpserver.Routes{
		Hook: hookHandler,
	}, buildinfo.Current())

	return &App{server: server, closers: append([]appCloser(nil), closers...)}, nil
}

func validatePasswordEncryptionConfig(cfg config.Config) error {
	switch {
	case cfg.PasswordEncryptionKeyB64 == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_B64 is required")
	case cfg.PasswordEncryptionKeyID == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_ID is required")
	default:
		return nil
	}
}

func (a *App) Run(ctx context.Context) error {
	var runtimeErr error
	if a.worker == nil {
		runtimeErr = a.server.Run(ctx)
	} else {
		runtimeErr = a.runServerAndWorker(ctx)
	}
	return errors.Join(runtimeErr, closeAppResources(ctx, a.closers))
}

func (a *App) runServerAndWorker(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- a.server.Run(runCtx)
	}()
	go func() {
		errCh <- a.worker.Run(runCtx)
	}()

	var runtimeErr error
	for completed := 0; completed < 2; completed++ {
		err := <-errCh
		cancel()
		if err != nil && runtimeErr == nil {
			runtimeErr = err
		}
	}
	return runtimeErr
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.server.ServeHTTP(w, r)
}

func closeAfterWiringError(err error, closers []appCloser) error {
	return errors.Join(err, closeAppResources(context.Background(), closers))
}

func closeAppResources(ctx context.Context, closers []appCloser) error {
	if len(closers) == 0 {
		return nil
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queueCloseTimeout)
	defer cancel()
	var closeErrs []error
	for _, closer := range closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(closeCtx); err != nil {
			closeErrs = append(closeErrs, err)
		}
	}
	return errors.Join(closeErrs...)
}
