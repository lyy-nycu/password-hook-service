package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nycu/password-hook-service/pkg/problem"
)

type NonceStore interface {
	Use(nonce string, ttl time.Duration) bool
}

type HMAC struct {
	secret []byte
	nonces NonceStore
	skew   time.Duration
}

func NewHMAC(secret string, nonces NonceStore, skew time.Duration) HMAC {
	return HMAC{
		secret: []byte(secret),
		nonces: nonces,
		skew:   skew,
	}
}

func (m HMAC) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeUnauthorized(w, r, "failed to read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		timestampHeader := r.Header.Get("X-Hook-Timestamp")
		nonce := r.Header.Get("X-Hook-Nonce")
		signature := r.Header.Get("X-Hook-Signature")
		timestamp, err := strconv.ParseInt(timestampHeader, 10, 64)
		if err != nil || nonce == "" || signature == "" {
			writeUnauthorized(w, r, "missing or invalid signature headers")
			return
		}

		if absDuration(time.Since(time.Unix(timestamp, 0))) > m.skew {
			writeUnauthorized(w, r, "signature timestamp is outside allowed skew")
			return
		}
		if m.nonces != nil && !m.nonces.Use(nonce, 60*time.Second) {
			writeUnauthorized(w, r, "nonce has already been used")
			return
		}
		if !m.validSignature(timestampHeader, nonce, body, signature) {
			writeUnauthorized(w, r, "signature mismatch")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (m HMAC) validSignature(timestamp string, nonce string, body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}

	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(fmt.Sprintf("%s.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)
	want := mac.Sum(nil)

	return hmac.Equal(got, want)
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	problem.Write(w, problem.New(
		"https://nycu.edu.tw/problems/unauthorized",
		"Unauthorized",
		http.StatusUnauthorized,
		detail,
		r.URL.Path,
		"",
	))
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

type MemoryNonceStore struct {
	mu     sync.Mutex
	seenAt map[string]time.Time
}

func NewMemoryNonceStore(_ time.Duration) *MemoryNonceStore {
	return &MemoryNonceStore{seenAt: map[string]time.Time{}}
}

func (s *MemoryNonceStore) Use(nonce string, ttl time.Duration) bool {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, seen := range s.seenAt {
		if now.Sub(seen) > ttl {
			delete(s.seenAt, key)
		}
	}
	if _, exists := s.seenAt[nonce]; exists {
		return false
	}
	s.seenAt[nonce] = now
	return true
}
