package observability

import (
	"fmt"
	"log/slog"
)

const (
	MetricHookRequestsTotal       = "hook_requests_total"
	MetricMigrationSkippedTotal   = "migration_skipped_total"
	MetricMiddlewareRequestsTotal = "middleware_requests_total"
	MetricWorkerMessagesTotal     = "worker_messages_total"
	MetricGraphUpsertDuration     = "graph_upsert_duration_seconds"
	MetricQueueDepth              = "queue_depth"

	ActionHookAccepted        = "hook_password_sync_accepted"
	ActionHookSkipped         = "hook_password_sync_skipped"
	ActionHookRejected        = "hook_password_sync_rejected"
	ActionMiddlewareRejected  = "middleware_request_rejected"
	ActionMiddlewareRecovered = "middleware_panic_recovered"
	ActionWorkerCompleted     = "worker_password_sync_completed"
	ActionWorkerFailed        = "worker_password_sync_failed"
	ActionWorkerInvalid       = "worker_message_invalid"
	ActionWorkerAbandoned     = "worker_message_abandoned"
	ActionGraphUpsert         = "graph_password_upsert"
	ActionQueueDepthProbe     = "queue_depth_probe"
)

type SafeIdentity struct {
	TraceID      string
	CN           string
	UPN          string
	EventType    string
	IdentityType string
}

func SafeIdentityAttrs(identity SafeIdentity) []slog.Attr {
	attrs := make([]slog.Attr, 0, 5)
	if identity.TraceID != "" {
		attrs = append(attrs, slog.String("traceId", identity.TraceID))
	}
	if identity.CN != "" {
		attrs = append(attrs, slog.String("cn", identity.CN))
	}
	if identity.UPN != "" {
		attrs = append(attrs, slog.String("upn", identity.UPN))
	}
	if identity.EventType != "" {
		attrs = append(attrs, slog.String("eventType", identity.EventType))
	}
	if identity.IdentityType != "" {
		attrs = append(attrs, slog.String("identityType", identity.IdentityType))
	}
	return attrs
}

func LabelsFromAttrs(attrs []slog.Attr) Labels {
	labels := make(Labels, len(attrs))
	for _, attr := range attrs {
		labels[attr.Key] = fmt.Sprint(attr.Value.Any())
	}
	return labels
}
