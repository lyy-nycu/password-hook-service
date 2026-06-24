package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHMACAllowsValidSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	signature := sign(secret, timestamp, nonce, body)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})

	middleware := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+signature)

	middleware.Wrap(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func sign(secret string, timestamp int64, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
