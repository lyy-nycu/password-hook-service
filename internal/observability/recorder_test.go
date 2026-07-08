package observability

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestNoopRecorderAcceptsAllSignals(t *testing.T) {
	recorder := NoopRecorder{}

	recorder.Inc(context.Background(), MetricHookRequestsTotal, Labels{"status": "202"})
	recorder.ObserveDuration(context.Background(), MetricGraphUpsertDuration, 12*time.Millisecond, Labels{"outcome": "success"})
	recorder.SetGauge(context.Background(), MetricQueueDepth, 7, Labels{"queue": "password-sync"})
}

func TestCaptureRecorderCopiesLabels(t *testing.T) {
	recorder := NewCaptureRecorder()
	labels := Labels{"status": "202", "outcome": "enqueued"}

	recorder.Inc(context.Background(), MetricHookRequestsTotal, labels)
	labels["status"] = "500"

	got := recorder.Counters(MetricHookRequestsTotal)
	if len(got) != 1 {
		t.Fatalf("counter samples = %d, want 1", len(got))
	}
	if got[0].Labels["status"] != "202" {
		t.Fatalf("stored status = %q, want original 202", got[0].Labels["status"])
	}
}

func TestSafeIdentityAttrsOmitSecrets(t *testing.T) {
	attrs := SafeIdentityAttrs(SafeIdentity{
		TraceID:      "trace-123",
		CN:           "311551001",
		UPN:          "311551001@nycu.edu.tw",
		EventType:    "password_change",
		IdentityType: "student_id",
	})

	got := map[string]any{}
	for _, attr := range attrs {
		got[attr.Key] = attr.Value.Any()
	}
	if got["traceId"] != "trace-123" || got["eventType"] != "password_change" {
		t.Fatalf("attrs = %#v, want traceId and eventType", attrs)
	}
	for _, forbidden := range []string{"password", "passwordCiphertext", "passwordNonce", "passwordKeyId"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("attrs contained forbidden key %q: %#v", forbidden, got)
		}
	}
}

func TestLabelsFromAttrsKeepsStableStringValues(t *testing.T) {
	labels := LabelsFromAttrs([]slog.Attr{
		slog.String("eventType", "login_bootstrap"),
		slog.Int("attempts", 4),
	})

	if labels["eventType"] != "login_bootstrap" || labels["attempts"] != "4" {
		t.Fatalf("labels = %#v, want stringified attr values", labels)
	}
}
