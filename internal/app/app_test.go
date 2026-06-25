package app

import (
	"bytes"
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
)

func TestAppHookRouteWritesAccessLog(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	cfg := config.Config{
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
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
	if !bytes.Contains(logs.Bytes(), []byte(`"traceId":"trace-123"`)) {
		t.Fatalf("logs missing traceId: %s", logs.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"status":202`)) {
		t.Fatalf("logs missing status: %s", logs.String())
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
