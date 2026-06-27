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

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

func completeAppConfig() config.Config {
	return config.Config{
		SecretsSource:              config.SecretsSourceEnv,
		KeyVaultURL:                "",
		KeyVaultSecretNames:        config.KeyVaultSecretNames{HMACSecret: "hook-hmac-secret", ServiceBusConnectionString: "servicebus-conn-str"},
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		EntraFallbackDomain:        "nycu.onmicrosoft.com",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		PortalAllowedCIDRs:         nil,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
		GraphTenantID:              "tenant-id",
		GraphClientID:              "client-id",
		GraphClientSecret:          "graph-client-secret",
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
