package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type NonceStore interface {
	Use(nonce string, ttl time.Duration) bool
}

type HMAC struct {
	secret      []byte
	nonces      NonceStore
	skew        time.Duration
	nonceTTL    time.Duration
	problemBase string
}

func NewHMAC(secret string, nonces NonceStore, skew time.Duration) (*HMAC, error) {
	return NewHMACWithProblemBase(secret, nonces, skew, problem.DefaultBaseURL)
}

func NewHMACWithProblemBase(secret string, nonces NonceStore, skew time.Duration, problemBase string) (*HMAC, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("hmac secret is required")
	}
	if skew <= 0 {
		return nil, errors.New("hmac clock skew must be positive")
	}
	return &HMAC{
		secret:      []byte(secret),
		nonces:      nonces,
		skew:        skew,
		nonceTTL:    nonceTTL(nonces),
		problemBase: problemBase,
	}, nil
}

func (m HMAC) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			m.writeUnauthorized(w, r, "failed to read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		timestampHeader := r.Header.Get("X-Hook-Timestamp")
		nonce := r.Header.Get("X-Hook-Nonce")
		signature := r.Header.Get("X-Hook-Signature")
		timestamp, err := strconv.ParseInt(timestampHeader, 10, 64)
		if err != nil || nonce == "" || signature == "" {
			m.writeUnauthorized(w, r, "missing or invalid signature headers")
			return
		}

		if absDuration(time.Since(time.Unix(timestamp, 0))) > m.skew {
			m.writeUnauthorized(w, r, "signature timestamp is outside allowed skew")
			return
		}
		if !m.validSignature(timestampHeader, nonce, body, signature) {
			m.writeUnauthorized(w, r, "signature mismatch")
			return
		}
		if m.nonces != nil && !m.nonces.Use(nonce, m.nonceTTL) {
			m.writeUnauthorized(w, r, "nonce has already been used")
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

func (m HMAC) writeUnauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	problem.Write(w, problem.Unauthorized(m.problemBase, r.URL.Path, requestid.From(r.Context()), detail))
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

type MemoryNonceStore struct {
	ttl    time.Duration
	mu     sync.Mutex
	seenAt map[string]time.Time
}

func NewMemoryNonceStore(ttl time.Duration) *MemoryNonceStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &MemoryNonceStore{ttl: ttl, seenAt: map[string]time.Time{}}
}

func (s *MemoryNonceStore) TTL() time.Duration {
	return s.ttl
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

func nonceTTL(nonces NonceStore) time.Duration {
	if store, ok := nonces.(interface{ TTL() time.Duration }); ok && store.TTL() > 0 {
		return store.TTL()
	}
	return 60 * time.Second
}
