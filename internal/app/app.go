package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/servicebusqueue"
)

const queueCloseTimeout = 5 * time.Second

type App struct {
	server *httpserver.Server
	closer interface {
		Close(context.Context) error
	}
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	queue, err := servicebusqueue.NewFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return nil, err
	}
	return newWithQueue(cfg, queue, queue)
}

func NewWithQueue(cfg config.Config, queue migration.Queue) (*App, error) {
	if err := cfg.ValidateHTTP(); err != nil {
		return nil, err
	}
	if queue == nil {
		return nil, errors.New("migration queue is required")
	}
	return newWithQueue(cfg, queue, nil)
}

func newWithQueue(cfg config.Config, queue migration.Queue, closer interface{ Close(context.Context) error }) (*App, error) {
	service := migration.NewService(cfg.EntraPrimaryDomain, queue)
	hook := handler.NewHook(service, cfg.ProblemBaseURL)
	hmacMiddleware, err := middleware.NewHMACWithProblemBase(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, cfg.ProblemBaseURL)
	if err != nil {
		if closer == nil {
			return nil, err
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), queueCloseTimeout)
		defer cancel()
		return nil, errors.Join(err, closer.Close(closeCtx))
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

	return &App{server: server, closer: closer}, nil
}

func (a *App) Run(ctx context.Context) error {
	err := a.server.Run(ctx)
	if a.closer == nil {
		return err
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queueCloseTimeout)
	defer cancel()
	closeErr := a.closer.Close(closeCtx)
	return errors.Join(err, closeErr)
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.server.ServeHTTP(w, r)
}
