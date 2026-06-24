package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nycu/password-hook-service/internal/app"
	"github.com/nycu/password-hook-service/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application := app.New(config.Load())
	if err := application.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
