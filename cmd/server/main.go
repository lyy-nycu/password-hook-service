package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nycu/password-hook-service/internal/app"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/secretloader"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := secretloader.Resolve(ctx, config.Load(), nil)
	if err != nil {
		slog.Error("load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	application, err := app.New(cfg)
	if err != nil {
		slog.Error("invalid configuration", slog.Any("error", err))
		os.Exit(1)
	}
	if err := application.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
