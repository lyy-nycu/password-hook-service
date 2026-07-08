package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nycu/password-hook-service/internal/observability"
)

func recordMiddlewareOutcome(ctx context.Context, logger *slog.Logger, recorder observability.Recorder, traceID string, middlewareName string, status int, outcome string, reason string) {
	if recorder == nil {
		recorder = observability.NoopRecorder{}
	}
	labels := observability.Labels{
		"middleware": middlewareName,
		"status":     fmt.Sprint(status),
		"outcome":    outcome,
	}
	if reason != "" {
		labels["reason"] = reason
	}
	recorder.Inc(ctx, observability.MetricMiddlewareRequestsTotal, labels)
	if logger == nil {
		return
	}
	action := observability.ActionMiddlewareRejected
	if outcome == "panic_recovered" {
		action = observability.ActionMiddlewareRecovered
	}
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("traceId", traceID),
		slog.String("middleware", middlewareName),
		slog.Int("status", status),
		slog.String("outcome", outcome),
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, action, attrs...)
}
