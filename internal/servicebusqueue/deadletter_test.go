package servicebusqueue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/worker"
)

func TestDeadLetterQueueSendsSanitizedPasswordSyncFailure(t *testing.T) {
	ctx := context.Background()
	sender := &captureSender{}
	queue, err := NewDeadLetterQueue(sender)
	if err != nil {
		t.Fatalf("NewDeadLetterQueue returned error: %v", err)
	}

	err = queue.RecordPasswordSyncFailure(ctx, worker.DeadLetterEntry{
		Kind:        "password-sync",
		CN:          "u1234567",
		UPN:         "u1234567@example.edu",
		Reason:      worker.DeadLetterReasonTransientRetriesExhausted,
		Description: "transient processor retries exhausted",
		Attempts:    4,
		EnqueuedAt:  time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC),
		FailedAt:    time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC),
		Password:    "must-not-appear",
	})
	if err != nil {
		t.Fatalf("RecordPasswordSyncFailure returned error: %v", err)
	}

	if sender.sent != 1 {
		t.Fatalf("sent = %d, want 1", sender.sent)
	}
	got := sender.message
	if got == nil {
		t.Fatal("sent message is nil")
	}
	if got.Subject == nil || *got.Subject != "password-sync-dlq" {
		t.Fatalf("Subject = %v, want password-sync-dlq", got.Subject)
	}
	if got.ContentType == nil || *got.ContentType != "application/json" {
		t.Fatalf("ContentType = %v, want application/json", got.ContentType)
	}
	if got.ApplicationProperties["kind"] != "password-sync-dlq" {
		t.Fatalf("kind property = %v, want password-sync-dlq", got.ApplicationProperties["kind"])
	}
	if got.ApplicationProperties["cn"] != "u1234567" {
		t.Fatalf("cn property = %v, want u1234567", got.ApplicationProperties["cn"])
	}
	if got.ApplicationProperties["upn"] != "u1234567@example.edu" {
		t.Fatalf("upn property = %v, want u1234567@example.edu", got.ApplicationProperties["upn"])
	}
	if got.ApplicationProperties["reason"] != worker.DeadLetterReasonTransientRetriesExhausted {
		t.Fatalf("reason property = %v, want %s", got.ApplicationProperties["reason"], worker.DeadLetterReasonTransientRetriesExhausted)
	}
	body := string(got.Body)
	if !strings.Contains(body, `"attempts":4`) {
		t.Fatalf("body = %s, want attempts 4", body)
	}
	if strings.Contains(body, "must-not-appear") || strings.Contains(body, `"password"`) {
		t.Fatalf("body leaked password data: %s", body)
	}
	for key, value := range got.ApplicationProperties {
		if strings.Contains(key, "password") {
			t.Fatalf("application property key leaked password metadata: %q", key)
		}
		if text, ok := value.(string); ok && strings.Contains(text, "must-not-appear") {
			t.Fatalf("application property %q leaked password value", key)
		}
	}
}

func TestSafeDLQDepthProbeRecordsDepth(t *testing.T) {
	t.Parallel()

	reader := &fakeDepthReader{depths: map[string]int64{"password-sync-dlq": 3}}
	recorder := observability.NewCaptureRecorder()
	probe := NewSafeDLQDepthProbe(reader, "password-sync-dlq", recorder)

	depth, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if depth != 3 {
		t.Fatalf("depth = %d, want 3", depth)
	}
	samples := recorder.Gauges(observability.MetricQueueDepth)
	if len(samples) != 1 || samples[0].Labels["kind"] != "safe_dlq" {
		t.Fatalf("samples = %#v, want safe_dlq queue depth", samples)
	}
}

func TestNewDeadLetterQueueRejectsNilSender(t *testing.T) {
	queue, err := NewDeadLetterQueue(nil)
	if err == nil {
		t.Fatal("NewDeadLetterQueue returned nil error")
	}
	if queue != nil {
		t.Fatalf("NewDeadLetterQueue queue = %#v, want nil", queue)
	}
	if err.Error() != "service bus dead-letter sender is required" {
		t.Fatalf("NewDeadLetterQueue error = %q, want service bus dead-letter sender is required", err.Error())
	}
}

func TestNewDeadLetterQueueFromNamespaceRejectsEmptyNamespace(t *testing.T) {
	t.Parallel()

	queue, err := NewDeadLetterQueueFromNamespace("", fakeTokenCredential{}, "password-sync-dlq")
	if err == nil || err.Error() != "service bus namespace FQDN is required" {
		t.Fatalf("NewDeadLetterQueueFromNamespace error = %v, want namespace error", err)
	}
	if queue != nil {
		t.Fatalf("NewDeadLetterQueueFromNamespace queue = %#v, want nil", queue)
	}
}

