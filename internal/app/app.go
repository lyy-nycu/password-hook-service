package app

import (
	"context"

	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/requestid"
)

type App struct {
	server *httpserver.Server
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	queue := discardQueue{}
	service := migration.NewService(cfg.EntraPrimaryDomain, queue)
	hook := handler.NewHook(service, cfg.ProblemBaseURL)
	hmacMiddleware := middleware.NewHMAC(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew)

	server := httpserver.New(cfg.HTTPAddr, httpserver.Routes{
		Hook: requestid.Middleware(hmacMiddleware.Wrap(hook)),
	}, buildinfo.Current())

	return &App{server: server}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.server.Run(ctx)
}

type discardQueue struct{}

func (discardQueue) EnqueuePasswordSync(context.Context, migration.PasswordSyncMessage) error {
	return nil
}
