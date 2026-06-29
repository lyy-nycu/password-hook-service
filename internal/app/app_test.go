package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/migration"
)

const testServiceBusConnectionString = "servicebus-connection-string-for-tests"

func TestAppHookRouteEnqueuesInternalIdentity(t *testing.T) {
	logs, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Request-ID", "trace-123")
	signRequest(req, cfg.HMACSecret, body)
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	if queue.messages[0].UPN != "311551001@nycu.edu.tw" {
		t.Fatalf("queued UPN = %q", queue.messages[0].UPN)
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"traceId":"trace-123"`)) {
		t.Fatalf("logs missing traceId: %s", logs.String())
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("logs leaked password: %s", logs.String())
	}
}

func TestAppHookRouteSkipsExternalEmailWithoutEnqueue(t *testing.T) {
	_, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 0 {
		t.Fatalf("queued %d messages, want 0", len(queue.messages))
	}
}

func TestNewWithQueueClosesOwnedQueueWhenAppWiringFails(t *testing.T) {
	cfg := completeAppConfig()
	cfg.HMACSecret = ""
	closer := &captureCloser{}

	application, err := newWithQueue(cfg, &captureQueue{}, closer)
	if err == nil {
		t.Fatal("newWithQueue returned nil error")
	}
	if application != nil {
		t.Fatalf("newWithQueue returned app = %#v, want nil", application)
	}
	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
	if len(closer.closeContexts) != 1 {
		t.Fatalf("close contexts = %d, want 1", len(closer.closeContexts))
	}
	if closer.closeContexts[0] == nil {
		t.Fatal("close context is nil")
	}
}

func TestNewWithQueueDoesNotRequireServiceBusConfiguration(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusQueueName = ""

	application, err := NewWithQueue(cfg, &captureQueue{})

	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}
	if application == nil {
		t.Fatal("NewWithQueue returned nil app")
	}
}

func TestRunClosesQueueWithBoundedContextFromCallerContext(t *testing.T) {
	closer := &captureCloser{}
	cfg := completeAppConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	application, err := newWithQueue(cfg, &captureQueue{}, closer)
	if err != nil {
		t.Fatalf("newWithQueue returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
	if err := closer.closeErrs[0]; err != nil {
		t.Fatalf("close context err = %v, want nil", err)
	}
	if !closer.closeHadDeadlines[0] {
		t.Fatal("close context has no deadline")
	}
}

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

type captureCloser struct {
	closeCalls        int
	closeContexts     []context.Context
	closeErrs         []error
	closeHadDeadlines []bool
}

func (c *captureCloser) Close(ctx context.Context) error {
	c.closeCalls++
	c.closeContexts = append(c.closeContexts, ctx)
	c.closeErrs = append(c.closeErrs, ctx.Err())
	_, hasDeadline := ctx.Deadline()
	c.closeHadDeadlines = append(c.closeHadDeadlines, hasDeadline)
	return nil
}

func completeAppConfig() config.Config {
	return config.Config{
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		PortalAllowedCIDRs:         nil,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: testServiceBusConnectionString,
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
}

func captureDefaultLogger() (*bytes.Buffer, func()) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	return &logs, func() {
		slog.SetDefault(previous)
	}
}

func signRequest(req *http.Request, secret string, body []byte) {
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)

	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
}
