// Package syncstatus tracks each portal identity's password sync state so
// repeated login_bootstrap events don't redundantly enqueue an already-synced
// account. See design spec section 1.2.1 Amendment.
package syncstatus

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Status represents the last known sync outcome for a UPN. The zero value,
// StatusUnsynced, is also what Get returns for a UPN with no record yet.
type Status string

const (
	// StatusUnsynced means no sync has ever completed or is in flight for
	// this UPN (or the store simply has no record for it yet).
	StatusUnsynced Status = ""
	// StatusPending means a sync message was enqueued and the worker has
	// not yet reported an outcome.
	StatusPending Status = "sync_pending"
	// StatusSynced means the worker successfully processed the sync.
	StatusSynced Status = "synced"
	// StatusFailed means the worker exhausted retries or hit a permanent
	// error while processing the sync.
	StatusFailed Status = "sync_failed"
)

// Record is a snapshot of a UPN's sync status at a point in time.
// SourceEnqueuedAt is the EnqueuedAt timestamp of the message that produced
// this record, used to reject stale, out-of-order writes.
type Record struct {
	Status           Status
	UpdatedAt        time.Time
	SourceEnqueuedAt time.Time
}

// Store tracks per-UPN sync status. Implementations must be safe for
// concurrent use. Every Mark* method accepts the triggering message's own
// sourceEnqueuedAt timestamp; implementations must ignore (not error on) a
// write whose sourceEnqueuedAt is older than the currently-stored record's
// SourceEnqueuedAt, so a slow, out-of-order completion can never overwrite
// a newer outcome.
type Store interface {
	// Get returns the current record for upn. A UPN with no record yet
	// returns the zero-value Record (Status == StatusUnsynced), not an
	// error.
	Get(ctx context.Context, upn string) (Record, error)
	// MarkPending records that a sync message was just enqueued for upn.
	MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
	// MarkSynced records that upn was successfully synced.
	MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
	// MarkFailed records that syncing upn failed permanently or after
	// exhausting retries.
	MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
}

// MemoryStore is an in-process, non-durable Store implementation backed by
// a mutex-guarded map. See package doc for durability caveats.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
	now     func() time.Time
}

// NewMemoryStore returns a ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]Record),
		now:     time.Now,
	}
}

func (s *MemoryStore) Get(ctx context.Context, upn string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[normalizeUPN(upn)], nil
}

func (s *MemoryStore) MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusPending, sourceEnqueuedAt)
}

func (s *MemoryStore) MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusSynced, sourceEnqueuedAt)
}

func (s *MemoryStore) MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusFailed, sourceEnqueuedAt)
}

// set applies the ordering guard described on the Store interface: a write
// whose sourceEnqueuedAt is older than the stored record's SourceEnqueuedAt
// is silently dropped rather than applied or reported as an error.
func (s *MemoryStore) set(ctx context.Context, upn string, status Status, sourceEnqueuedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeUPN(upn)
	if existing, found := s.records[key]; found {
		switch {
		case sourceEnqueuedAt.Before(existing.SourceEnqueuedAt):
			return nil
		case sourceEnqueuedAt.Equal(existing.SourceEnqueuedAt) && statusPrecedence(status) <= statusPrecedence(existing.Status):
			return nil
		}
	}
	s.records[key] = Record{Status: status, UpdatedAt: s.now().UTC(), SourceEnqueuedAt: sourceEnqueuedAt.UTC()}
	return nil
}

func normalizeUPN(upn string) string {
	return strings.ToLower(strings.TrimSpace(upn))
}

func statusPrecedence(status Status) int {
	switch status {
	case StatusPending:
		return 1
	case StatusFailed:
		return 2
	case StatusSynced:
		return 3
	default:
		return 0
	}
}
