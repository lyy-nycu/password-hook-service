package graphprocessor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/worker"
)

func TestProcessorMapsWorkerCommandToGraphUser(t *testing.T) {
	graph := &captureGraphClient{}
	processor, err := New(graph)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	password := []byte("cleartext-password")
	command := worker.PasswordSyncCommand{
		CN:          "cn-must-not-be-sent",
		UPN:         "311551001@nycu.edu.tw",
		DisplayName: "Student One",
		Mail:        "student@nycu.edu.tw",
		Password:    password,
	}

	err = processor.ProcessPasswordSync(context.Background(), command)

	if err != nil {
		t.Fatalf("ProcessPasswordSync returned error: %v", err)
	}
	if graph.calls != 1 {
		t.Fatalf("graph calls = %d, want 1", graph.calls)
	}
	if graph.user.UPN != command.UPN {
		t.Fatalf("graph user UPN = %q, want %q", graph.user.UPN, command.UPN)
	}
	if graph.user.DisplayName != command.DisplayName {
		t.Fatalf("graph user DisplayName = %q, want %q", graph.user.DisplayName, command.DisplayName)
	}
	if graph.user.Mail != command.Mail {
		t.Fatalf("graph user Mail = %q, want %q", graph.user.Mail, command.Mail)
	}
	if len(graph.password) == 0 || &graph.password[0] != &password[0] {
		t.Fatal("processor did not pass the borrowed password buffer to graph client")
	}
}

func TestProcessorMapsPermanentGraphErrorToWorkerPermanentError(t *testing.T) {
	graphErr := &graphclient.PermanentError{StatusCode: 400, Operation: "patch user"}
	processor, err := New(&captureGraphClient{err: graphErr})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
		UPN:      "311551001@nycu.edu.tw",
		Password: []byte("cleartext-password"),
	})

	var permanent *worker.PermanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("ProcessPasswordSync error = %T %[1]v, want *worker.PermanentError", err)
	}
	if permanent.Reason != worker.PermanentReasonProcessorError {
		t.Fatalf("permanent reason = %q, want %q", permanent.Reason, worker.PermanentReasonProcessorError)
	}
	if !errors.Is(permanent.Err, graphErr) {
		t.Fatalf("wrapped error = %v, want graph permanent error", permanent.Err)
	}
}

func TestProcessorRecordsGraphSuccessDuration(t *testing.T) {
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	processor, err := NewWithOptions(&captureGraphClient{}, Options{
		Recorder: recorder,
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Now:      fixedClock(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), time.Date(2026, 7, 8, 12, 0, 0, 25*int(time.Millisecond), time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}

	err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
		TraceID:     "trace-123",
		UPN:         "311551001@nycu.edu.tw",
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
		Password:    []byte("cleartext-password"),
	})
	if err != nil {
		t.Fatalf("ProcessPasswordSync returned error: %v", err)
	}

	samples := recorder.Durations(observability.MetricGraphUpsertDuration)
	if len(samples) != 1 {
		t.Fatalf("duration samples = %d, want 1", len(samples))
	}
	if samples[0].Labels["outcome"] != "success" {
		t.Fatalf("labels = %#v, want success", samples[0].Labels)
	}
	if samples[0].Duration != 25*time.Millisecond {
		t.Fatalf("duration = %s, want 25ms", samples[0].Duration)
	}
	gotLogs := logs.String()
	if !strings.Contains(gotLogs, observability.ActionGraphUpsert) || !strings.Contains(gotLogs, `"traceId":"trace-123"`) {
		t.Fatalf("logs = %s, want graph upsert with traceId", gotLogs)
	}
	if strings.Contains(gotLogs, "cleartext-password") {
		t.Fatalf("logs leaked password: %s", gotLogs)
	}
}

func TestProcessorRecordsGraphPermanentAndTransientOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantLabel   string
		wantWorker  bool
		leakedToken string
	}{
		{
			name:        "permanent",
			err:         &graphclient.PermanentError{StatusCode: 403, Operation: "patch user", Err: errors.New("insufficient privileges for tenant abc123")},
			wantLabel:   "permanent_error",
			wantWorker:  true,
			leakedToken: "insufficient privileges for tenant abc123",
		},
		{
			name:        "transient",
			err:         &graphclient.TransientError{StatusCode: 503, Operation: "patch user", Err: errors.New("upstream timeout id xyz789")},
			wantLabel:   "transient_error",
			leakedToken: "upstream timeout id xyz789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := observability.NewCaptureRecorder()
			var logs bytes.Buffer
			processor, err := NewWithOptions(&captureGraphClient{err: tt.err}, Options{
				Recorder: recorder,
				Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatalf("NewWithOptions returned error: %v", err)
			}

			err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
				UPN:      "311551001@nycu.edu.tw",
				Password: []byte("cleartext-password"),
			})

			var permanent *worker.PermanentError
			if gotPermanent := errors.As(err, &permanent); gotPermanent != tt.wantWorker {
				t.Fatalf("worker permanent = %v, want %v; err=%v", gotPermanent, tt.wantWorker, err)
			}
			samples := recorder.Durations(observability.MetricGraphUpsertDuration)
			if len(samples) != 1 || samples[0].Labels["outcome"] != tt.wantLabel {
				t.Fatalf("samples = %#v, want outcome %s", samples, tt.wantLabel)
			}
			gotLogs := logs.String()
			if !strings.Contains(gotLogs, observability.ActionGraphUpsert) || !strings.Contains(gotLogs, tt.wantLabel) {
				t.Fatalf("logs = %s, want graph upsert outcome %s", gotLogs, tt.wantLabel)
			}
			if strings.Contains(gotLogs, tt.leakedToken) {
				t.Fatalf("logs leaked graph error text: %s", gotLogs)
			}
		})
	}
}

func TestProcessorLeavesTransientGraphErrorRetryable(t *testing.T) {
	graphErr := &graphclient.TransientError{StatusCode: 503, Operation: "patch user"}
	processor, err := New(&captureGraphClient{err: graphErr})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
		UPN:      "311551001@nycu.edu.tw",
		Password: []byte("cleartext-password"),
	})

	if !errors.Is(err, graphErr) {
		t.Fatalf("ProcessPasswordSync error = %T %[1]v, want original graph transient error", err)
	}
	var permanent *worker.PermanentError
	if errors.As(err, &permanent) {
		t.Fatalf("ProcessPasswordSync returned permanent error for transient graph error: %v", err)
	}
}

func fixedClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		if i >= len(times) {
			return times[len(times)-1]
		}
		got := times[i]
		i++
		return got
	}
}

type captureGraphClient struct {
	calls    int
	user     graphclient.User
	password []byte
	err      error
}

func (c *captureGraphClient) UpsertUserPassword(_ context.Context, user graphclient.User, password []byte) error {
	c.calls++
	c.user = user
	c.password = password
	return c.err
}
