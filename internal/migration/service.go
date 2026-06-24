package migration

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Queue interface {
	EnqueuePasswordSync(context.Context, PasswordSyncMessage) error
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
	now           func() time.Time
}

func NewService(primaryDomain string, queue Queue) *Service {
	return &Service{
		primaryDomain: primaryDomain,
		queue:         queue,
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

	msg := PasswordSyncMessage{
		CN:          strings.TrimSpace(req.CN),
		UPN:         upn,
		Password:    req.Password,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Mail:        strings.TrimSpace(req.Mail),
		EnqueuedAt:  s.now().UTC(),
	}
	if err := s.queue.EnqueuePasswordSync(ctx, msg); err != nil {
		return decision, err
	}

	decision.Enqueued = true
	return decision, nil
}
