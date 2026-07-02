package servicebusqueue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestDeadLetterQueueWrapsSendError(t *testing.T) {
	sendErr := errors.New("service bus send failed")
	queue, err := NewDeadLetterQueue(&captureSender{sendErr: sendErr})
	if err != nil {
		t.Fatalf("NewDeadLetterQueue returned error: %v", err)
	}

	err = queue.RecordPasswordSyncFailure(context.Background(), worker.DeadLetterEntry{
		Kind:     "password-sync",
		CN:       "u1234567",
		UPN:      "u1234567@example.edu",
		Reason:   worker.DeadLetterReasonPermanentProcessor,
		Attempts: 1,
		FailedAt: time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("RecordPasswordSyncFailure returned nil error")
	}
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v, want send error", err)
	}
	if !strings.Contains(err.Error(), "send password sync dead-letter message") {
		t.Fatalf("error = %q, want send password sync dead-letter message", err.Error())
	}
}
