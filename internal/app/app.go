package app

import (
	"context"
	"log/slog"
	"net/http"

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
	hmacMiddleware, err := middleware.NewHMACWithProblemBase(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, cfg.ProblemBaseURL)
	if err != nil {
		return nil, err
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

	return &App{server: server}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.server.Run(ctx)
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.server.ServeHTTP(w, r)
}

type discardQueue struct{}

func (discardQueue) EnqueuePasswordSync(context.Context, migration.PasswordSyncMessage) error {
	return nil
}
