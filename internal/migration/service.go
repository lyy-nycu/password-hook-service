package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

type Queue interface {
	EnqueuePasswordSync(context.Context, PasswordSyncMessage) error
}

type PasswordEncrypter interface {
	Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error)
}

type Request struct {
	CN          string
	Password    string
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
	primaryDomain string
	queue         Queue
	encrypter     PasswordEncrypter
	now           func() time.Time
}

func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter) *Service {
	return &Service{
		primaryDomain: primaryDomain,
		queue:         queue,
		encrypter:     encrypter,
		now:           time.Now,
	}
}

func (s *Service) Submit(ctx context.Context, req Request) (Decision, error) {
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

	if s.queue == nil {
		return decision, errors.New("migration queue is not configured")
	}
	if s.encrypter == nil {
		return decision, errors.New("password encrypter is not configured")
	}

	msg := PasswordSyncMessage{
		CN:          strings.TrimSpace(req.CN),
		UPN:         upn,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Mail:        strings.TrimSpace(req.Mail),
		EnqueuedAt:  s.now().UTC(),
	}
	passwordBytes := []byte(req.Password)
	defer passwordcrypto.ZeroBytes(passwordBytes)
	env, err := s.encrypter.Encrypt(ctx, passwordBytes, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
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

	decision.Enqueued = true
	return decision, nil
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
