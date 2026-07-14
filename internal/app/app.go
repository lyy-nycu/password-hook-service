package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/nycu/password-hook-service/internal/azuremonitor"
	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/graphprocessor"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/servicebusqueue"
	"github.com/nycu/password-hook-service/internal/syncstatus"
	"github.com/nycu/password-hook-service/internal/worker"
)

const (
	queueCloseTimeout               = 5 * time.Second
	redisPingTimeout                = 5 * time.Second
	azureMonitorMetricFlushInterval = time.Minute
	azureMonitorMetricFlushTimeout  = 5 * time.Second
)

type appWorker interface {
	Run(context.Context) error
}

type appCloser interface {
	Close(context.Context) error
}

type appCloserFunc func(context.Context) error

func (f appCloserFunc) Close(ctx context.Context) error {
	return f(ctx)
}

type metricFlusher interface {
	Flush(context.Context) error
}

type periodicMetricFlusher struct {
	flusher metricFlusher
	period  time.Duration
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newPeriodicMetricFlusher(flusher metricFlusher, period time.Duration) *periodicMetricFlusher {
	if period <= 0 {
		period = azureMonitorMetricFlushInterval
	}
	p := &periodicMetricFlusher{
		flusher: flusher,
		period:  period,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go p.run()
	return p
}

func (p *periodicMetricFlusher) run() {
	ticker := time.NewTicker(p.period)
	defer ticker.Stop()
	defer close(p.done)
	for {
		select {
		case <-ticker.C:
			p.flushWithTimeout()
		case <-p.stop:
			return
		}
	}
}

func (p *periodicMetricFlusher) flushWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), azureMonitorMetricFlushTimeout)
	defer cancel()
	if err := p.flusher.Flush(ctx); err != nil {
		slog.Warn("flush azure monitor metrics", slog.Any("error", err))
	}
}

func (p *periodicMetricFlusher) Close(ctx context.Context) error {
	p.once.Do(func() {
		close(p.stop)
	})
	<-p.done
	return p.flusher.Flush(ctx)
}

type passwordCodec interface {
	migration.PasswordEncrypter
	worker.PasswordDecrypter
}

type observabilityRuntime struct {
	recorder observability.Recorder
	closers  []appCloser
}

type serviceBusRuntime struct {
	queue    migration.Queue
	receiver worker.Receiver
	dlq      worker.DeadLetterSink
	closers  []appCloser
}

type syncStatusRuntime struct {
	store  syncstatus.Store
	closer appCloser
}

var buildServiceBusRuntime = newServiceBusRuntime
var buildSyncStatusRuntime = newSyncStatusRuntime

type App struct {
	server  *httpserver.Server
	worker  appWorker
	closers []appCloser
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	observabilityRuntime, err := newObservabilityRuntime(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	closers := append([]appCloser(nil), observabilityRuntime.closers...)

	serviceBus, err := buildServiceBusRuntime(cfg)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	closers = append(closers, serviceBus.closers...)

	syncStatus, err := buildSyncStatusRuntime(context.Background(), cfg)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	if syncStatus.closer != nil {
		closers = append(closers, syncStatus.closer)
	}
	if syncStatus.store == nil {
		return nil, closeAfterWiringError(errors.New("sync status store is required"), closers)
	}

	credential, err := azidentity.NewClientSecretCredential(cfg.GraphTenantID, cfg.GraphClientID, cfg.GraphClientSecret, nil)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	graph, err := graphclient.NewHTTPClient(credential, graphclient.Options{})
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}
	processor, err := graphprocessor.NewWithOptions(graph, graphprocessor.Options{
		Logger:   slog.Default(),
		Recorder: observabilityRuntime.recorder,
	})
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}

	passwordCodec, err := newPasswordCodec(cfg)
	if err != nil {
		return nil, closeAfterWiringError(err, closers)
	}

	return newWithWorkerDependenciesWithSyncStatusAndRecorder(cfg, serviceBus.queue, serviceBus.receiver, processor, serviceBus.dlq, passwordCodec, syncStatus.store, observabilityRuntime.recorder, closers...)
}

func newSyncStatusRuntime(ctx context.Context, cfg config.Config) (syncStatusRuntime, error) {
	switch cfg.SyncStatusStore {
	case config.SyncStatusStoreMemory:
		return syncStatusRuntime{store: syncstatus.NewMemoryStore()}, nil
	case config.SyncStatusStoreRedis:
		store, err := syncstatus.NewManagedIdentityRedisStore(ctx, syncstatus.RedisOptions{
			Host:                    cfg.RedisHost,
			Port:                    cfg.RedisPort,
			KeyPrefix:               cfg.RedisKeyPrefix,
			PendingTTL:              cfg.PasswordMessageTTL,
			TerminalTTL:             cfg.SyncStatusTerminalTTL,
			ManagedIdentityClientID: cfg.AzureClientID,
			PingTimeout:             redisPingTimeout,
		})
		if err != nil {
			return syncStatusRuntime{}, err
		}
		return syncStatusRuntime{store: store, closer: store}, nil
	default:
		return syncStatusRuntime{}, fmt.Errorf("unsupported sync status store %q", cfg.SyncStatusStore)
	}
}

func newObservabilityRuntime(ctx context.Context, cfg config.Config) (observabilityRuntime, error) {
	if cfg.ObservabilityExporter != config.ObservabilityExporterAzureMonitor {
		return observabilityRuntime{recorder: observability.NoopRecorder{}}, nil
	}
	shutdown, err := azuremonitor.SetupOTel(ctx, azuremonitor.OTelOptions{
		ServiceName:  "password-hook-service",
		OTLPEndpoint: cfg.OTLPExporterEndpoint,
	})
	if err != nil {
		return observabilityRuntime{}, err
	}
	metricCredential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return observabilityRuntime{}, errors.Join(err, shutdown(ctx))
	}
	recorder := azuremonitor.NewMetricRecorder(azuremonitor.MetricRecorderOptions{
		EndpointBaseURL: "https://" + cfg.AzureMonitorMetricRegion + ".monitoring.azure.com",
		ResourceID:      cfg.AzureMonitorMetricResourceID,
		Region:          cfg.AzureMonitorMetricRegion,
		Namespace:       cfg.AzureMonitorMetricNamespace,
		TokenSource:     azuremonitor.NewCredentialTokenSource(metricCredential, "https://monitoring.azure.com/.default"),
	})
	return observabilityRuntime{
		recorder: recorder,
		closers:  []appCloser{appCloserFunc(shutdown), newPeriodicMetricFlusher(recorder, azureMonitorMetricFlushInterval)},
	}, nil
}

func newServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	switch cfg.ServiceBusAuthMode {
	case "", config.ServiceBusAuthConnectionString:
		return newConnectionStringServiceBusRuntime(cfg)
	case config.ServiceBusAuthManagedIdentity:
		return newManagedIdentityServiceBusRuntime(cfg)
	default:
		return serviceBusRuntime{}, errors.New("SERVICEBUS_AUTH_MODE must be connection_string or managed_identity")
	}
}

func newConnectionStringServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	queue, err := servicebusqueue.NewFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return serviceBusRuntime{}, err
	}
	closers := []appCloser{queue}

	receiver, err := servicebusqueue.NewReceiverFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, receiver)

	dlq, err := servicebusqueue.NewDeadLetterQueueFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusDeadLetterQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, dlq)

	return serviceBusRuntime{queue: queue, receiver: receiver, dlq: dlq, closers: closers}, nil
}

func newManagedIdentityServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return serviceBusRuntime{}, fmt.Errorf("create Azure credential: %w", err)
	}

	queue, err := servicebusqueue.NewFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return serviceBusRuntime{}, err
	}
	closers := []appCloser{queue}

	receiver, err := servicebusqueue.NewReceiverFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, receiver)

	dlq, err := servicebusqueue.NewDeadLetterQueueFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusDeadLetterQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, dlq)

	return serviceBusRuntime{queue: queue, receiver: receiver, dlq: dlq, closers: closers}, nil
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
	passwordCodec, err := newPasswordCodec(cfg)
	if err != nil {
		return nil, err
	}
	return newWithQueue(cfg, queue, passwordCodec, syncstatus.NewMemoryStore())
}

func newWithWorkerDependencies(
	cfg config.Config,
	queue migration.Queue,
	receiver worker.Receiver,
	processor worker.Processor,
	deadLetterSink worker.DeadLetterSink,
	passwordCodec passwordCodec,
	closers ...appCloser,
) (*App, error) {
	return newWithWorkerDependenciesWithRecorder(cfg, queue, receiver, processor, deadLetterSink, passwordCodec, observability.NoopRecorder{}, closers...)
}

func newWithWorkerDependenciesWithRecorder(
	cfg config.Config,
	queue migration.Queue,
	receiver worker.Receiver,
	processor worker.Processor,
	deadLetterSink worker.DeadLetterSink,
	passwordCodec passwordCodec,
	recorder observability.Recorder,
	closers ...appCloser,
) (*App, error) {
	return newWithWorkerDependenciesWithSyncStatusAndRecorder(cfg, queue, receiver, processor, deadLetterSink, passwordCodec, syncstatus.NewMemoryStore(), recorder, closers...)
}

func newWithWorkerDependenciesWithSyncStatusAndRecorder(
	cfg config.Config,
	queue migration.Queue,
	receiver worker.Receiver,
	processor worker.Processor,
	deadLetterSink worker.DeadLetterSink,
	passwordCodec passwordCodec,
	syncStatusStore syncstatus.Store,
	recorder observability.Recorder,
	closers ...appCloser,
) (*App, error) {
	if passwordCodec == nil {
		return nil, errors.Join(errors.New("password codec is required"), closeAppResources(context.Background(), closers))
	}
	if syncStatusStore == nil {
		return nil, errors.Join(errors.New("sync status store is required"), closeAppResources(context.Background(), closers))
	}
	if recorder == nil {
		recorder = observability.NoopRecorder{}
	}
	application, err := newWithQueueWithRecorder(cfg, queue, passwordCodec, syncStatusStore, recorder, closers...)
	if err != nil {
		return nil, err
	}
	passwordWorker, err := worker.New(receiver, processor, worker.Options{
		DeadLetterSink:     deadLetterSink,
		PasswordDecrypter:  passwordCodec,
		SyncStatusRecorder: syncStatusStore,
		Logger:             slog.Default(),
		Recorder:           recorder,
	})
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	application.worker = passwordWorker
	return application, nil
}

func newWithQueue(cfg config.Config, queue migration.Queue, passwordEncrypter migration.PasswordEncrypter, syncStatusStore migration.SyncStatusStore, closers ...appCloser) (*App, error) {
	return newWithQueueWithRecorder(cfg, queue, passwordEncrypter, syncStatusStore, observability.NoopRecorder{}, closers...)
}

func newWithQueueWithRecorder(
	cfg config.Config,
	queue migration.Queue,
	passwordEncrypter migration.PasswordEncrypter,
	syncStatusStore migration.SyncStatusStore,
	recorder observability.Recorder,
	closers ...appCloser,
) (*App, error) {
	if passwordEncrypter == nil {
		return nil, errors.Join(errors.New("password encrypter is required"), closeAppResources(context.Background(), closers))
	}
	if recorder == nil {
		recorder = observability.NoopRecorder{}
	}
	service := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordEncrypter, migration.ServiceOptions{
		SyncStatusStore: syncStatusStore,
		// Reuse the queue message TTL as the pending-sync freshness window so
		// sync_pending cannot outlive the message it corresponds to.
		PendingTTL: cfg.PasswordMessageTTL,
	})
	hook := handler.NewHookWithOptions(service, cfg.ProblemBaseURL, handler.HookOptions{
		Logger:   slog.Default(),
		Recorder: recorder,
	})
	hmacMiddleware, err := middleware.NewHMACWithOptions(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, middleware.HMACOptions{
		ProblemBase:  cfg.ProblemBaseURL,
		MaxBodyBytes: cfg.HookMaxBodyBytes,
		Logger:       slog.Default(),
		Recorder:     recorder,
	})
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	rateLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		AllowedCIDRs:      cfg.PortalAllowedCIDRs,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		LimitPerIP:        cfg.RateLimitPerIP,
		Window:            cfg.RateLimitWindow,
		ProblemBase:       cfg.ProblemBaseURL,
		Logger:            slog.Default(),
		Recorder:          recorder,
	})

	hookHandler := hmacMiddleware.Wrap(hook)
	hookHandler = rateLimiter.Wrap(hookHandler)
	hookHandler = middleware.RecoveryWithOptions(middleware.RecoveryOptions{
		Logger:      slog.Default(),
		ProblemBase: cfg.ProblemBaseURL,
		Recorder:    recorder,
	})(hookHandler)
	hookHandler = middleware.AccessLog(slog.Default())(hookHandler)
	hookHandler = requestid.Middleware(hookHandler)

	server := httpserver.New(cfg.HTTPAddr, httpserver.Routes{
		Hook: hookHandler,
	}, buildinfo.Current())

	return &App{server: server, closers: append([]appCloser(nil), closers...)}, nil
}

func newPasswordCodec(cfg config.Config) (*passwordcrypto.Codec, error) {
	return passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
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
