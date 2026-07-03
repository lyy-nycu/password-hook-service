package graphprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/nycu/password-hook-service/internal/graphclient"
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
