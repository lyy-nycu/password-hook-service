package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/syncstatus"
)

type Queue interface {
	EnqueuePasswordSync(context.Context, PasswordSyncMessage) error
}

type PasswordEncrypter interface {
	Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error)
}

// defaultPendingTTL is the fallback used only when ServiceOptions.PendingTTL
// is unset (<= 0), e.g. by callers that don't wire config through. App wiring
// passes config.Config.PasswordMessageTTL explicitly so pending-sync freshness
// tracks the actual queue message TTL.
const defaultPendingTTL = 300 * time.Second

// SyncStatusStore is the narrow view of syncstatus.Store that Service needs.
// *syncstatus.MemoryStore satisfies this interface.
type SyncStatusStore interface {
	Get(ctx context.Context, upn string) (syncstatus.Record, error)
	MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
}

// ServiceOptions configures optional Service behavior.
type ServiceOptions struct {
	// SyncStatusStore, if non-nil, enables login_bootstrap dedupe. If nil,
	// every login_bootstrap event is enqueued unconditionally.
	SyncStatusStore SyncStatusStore
	// PendingTTL bounds how long a sync_pending record suppresses a repeat
	// login_bootstrap enqueue. Defaults to defaultPendingTTL when <= 0.
	PendingTTL time.Duration
}

type Request struct {
	CN string
	// EventType identifies why the caller invoked Submit. Must be one of
	// the EventType constants; Submit rejects any other value.
	EventType EventType
	// Password is borrowed mutable memory. Submit zeroes it before returning on
	// every success, skip, and error path; callers must not reuse it afterward.
	Password    []byte
	DisplayName string
	Mail        string
}

type Decision struct {
	IdentityType IdentityType
	UPN          string
	Enqueued     bool
	Skipped      bool
	Reason       string
}

type Service struct {
	primaryDomain   string
	queue           Queue
	encrypter       PasswordEncrypter
	now             func() time.Time
	syncStatusStore SyncStatusStore
	pendingTTL      time.Duration
}

// NewService constructs a Service. opts is variadic so existing call sites
// keep compiling unchanged; passing more than one ServiceOptions is invalid
// usage and only the first is honored.
func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter, opts ...ServiceOptions) *Service {
	var opt ServiceOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	pendingTTL := opt.PendingTTL
	if pendingTTL <= 0 {
		pendingTTL = defaultPendingTTL
	}
	return &Service{
		primaryDomain:   primaryDomain,
		queue:           queue,
		encrypter:       encrypter,
		now:             time.Now,
		syncStatusStore: opt.SyncStatusStore,
		pendingTTL:      pendingTTL,
	}
}

func (s *Service) Submit(ctx context.Context, req Request) (Decision, error) {
	defer passwordcrypto.ZeroBytes(req.Password)

	if !ValidEventType(req.EventType) {
		return Decision{}, ErrInvalidEventType
	}

	identityType := ClassifyCN(req.CN)
	decision := Decision{IdentityType: identityType}

	if identityType == IdentityExternalEmail {
		decision.Skipped = true
		decision.Reason = "cn_is_external_email"
		return decision, nil
	}
	if identityType == IdentityUnknown {
		return decision, ErrUnknownIdentity
	}

	upn, err := BuildUPN(req.CN, s.primaryDomain)
	if err != nil {
		return decision, err
	}
	decision.UPN = upn

	if req.EventType == EventLoginBootstrap && s.syncStatusStore != nil {
		if skip, reason := s.skipLoginBootstrap(ctx, upn); skip {
			decision.Skipped = true
			decision.Reason = reason
			return decision, nil
		}
	}

	if s.queue == nil {
		return decision, errors.New("migration queue is not configured")
	}
	if s.encrypter == nil {
		return decision, errors.New("password encrypter is not configured")
	}

	msg := PasswordSyncMessage{
		CN:          strings.TrimSpace(req.CN),
		UPN:         upn,
		EventType:   req.EventType,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Mail:        strings.TrimSpace(req.Mail),
		EnqueuedAt:  s.now().UTC(),
	}
	env, err := s.encrypter.Encrypt(ctx, req.Password, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
	if err != nil {
		return decision, fmt.Errorf("encrypt password payload: %w", err)
	}
	msg.PasswordCiphertext = env.Ciphertext
	msg.PasswordNonce = env.Nonce
	msg.PasswordKeyID = env.KeyID
	msg.PasswordAlg = env.Algorithm

	if err := s.queue.EnqueuePasswordSync(ctx, msg); err != nil {
		return decision, err
	}

	if s.syncStatusStore != nil {
		// Best-effort: status tracking must never fail an otherwise-successful
		// enqueue.
		_ = s.syncStatusStore.MarkPending(ctx, upn, msg.EnqueuedAt)
	}

	decision.Enqueued = true
	return decision, nil
}

// skipLoginBootstrap reports whether a login_bootstrap event for upn should
// be skipped because the account is already synced or has a fresh pending
// sync in flight. Store errors fail open so status tracking cannot block
// password bootstrap work.
func (s *Service) skipLoginBootstrap(ctx context.Context, upn string) (bool, string) {
	rec, err := s.syncStatusStore.Get(ctx, upn)
	if err != nil {
		return false, ""
	}
	switch rec.Status {
	case syncstatus.StatusSynced:
		return true, "already_synced"
	case syncstatus.StatusPending:
		if s.now().Sub(rec.UpdatedAt) < s.pendingTTL {
			return true, "sync_pending"
		}
		return false, ""
	default:
		return false, ""
	}
}

func PasswordAAD(cn string, upn string, enqueuedAt time.Time) []byte {
	return passwordAAD(cn, upn, enqueuedAt)
}

func passwordAAD(cn string, upn string, enqueuedAt time.Time) []byte {
	return []byte(strings.Join([]string{
		"password-sync",
		strings.TrimSpace(cn),
		strings.TrimSpace(upn),
		enqueuedAt.UTC().Format(time.RFC3339Nano),
	}, "\n"))
}
