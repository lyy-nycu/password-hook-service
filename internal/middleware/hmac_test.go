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

	middleware, err := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}
	rec := httptest.NewRecorder()
	req := signedRequest(body, timestamp, nonce, signature)

	middleware.Wrap(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func TestHMACRejectsReplayedNonce(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	signature := sign(secret, timestamp, nonce, body)
	store := NewMemoryNonceStore(60 * time.Second)
	middleware, err := NewHMAC(secret, store, 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	first := signedRequest(body, timestamp, nonce, signature)
	firstRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusAccepted)
	}

	second := signedRequest(body, timestamp, nonce, signature)
	secondRec := httptest.NewRecorder()
	middleware.Wrap(next).ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("second status = %d, want %d", secondRec.Code, http.StatusUnauthorized)
	}
}

func TestHMACRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	secret := "shared-secret"
	timestamp := time.Now().Add(-time.Minute).Unix()
	nonce := "abcdef0123456789abcdef0123456789"
	signature := sign(secret, timestamp, nonce, body)
	middleware, err := NewHMAC(secret, NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, signature))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHMACRejectsBadSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cn":"311551001"}`)
	timestamp := time.Now().Unix()
	nonce := "fedcba9876543210fedcba9876543210"
	middleware, err := NewHMAC("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err != nil {
		t.Fatalf("NewHMAC returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, signedRequest(body, timestamp, nonce, sign("wrong-secret", timestamp, nonce, body)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNewHMACRejectsEmptySecret(t *testing.T) {
	t.Parallel()

	_, err := NewHMAC("", NewMemoryNonceStore(60*time.Second), 30*time.Second)
	if err == nil {
		t.Fatal("NewHMAC returned nil error for empty secret")
	}
}

func signedRequest(body []byte, timestamp int64, nonce string, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+signature)
	return req
}

func sign(secret string, timestamp int64, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
