package syncstatus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreGetReturnsUnsyncedInitially(t *testing.T) {
	store := NewMemoryStore()

	rec, err := store.Get(context.Background(), "jdoe@example.edu")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rec.Status != StatusUnsynced {
		t.Errorf("Get() on unknown upn Status = %q, want %q", rec.Status, StatusUnsynced)
	}
	if !rec.UpdatedAt.IsZero() || !rec.SourceEnqueuedAt.IsZero() {
		t.Errorf("Get() on unknown upn = %+v, want zero-value timestamps", rec)
	}
}

func TestMemoryStoreTracksLifecycle(t *testing.T) {
	store := NewMemoryStore()
	upn := "jdoe@example.edu"
	ctx := context.Background()

	fixedNow := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }
	enqueuedAt := time.Date(2026, 7, 4, 11, 59, 0, 0, time.UTC)

	if err := store.MarkPending(ctx, upn, enqueuedAt); err != nil {
		t.Fatalf("MarkPending() error = %v", err)
	}
	rec, err := store.Get(ctx, upn)
	if err != nil {
		t.Fatalf("Get() after MarkPending error = %v", err)
	}
	if rec.Status != StatusPending {
		t.Errorf("Status after MarkPending = %q, want %q", rec.Status, StatusPending)
	}
	if !rec.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", rec.UpdatedAt, fixedNow)
	}
	if !rec.SourceEnqueuedAt.Equal(enqueuedAt) {
		t.Errorf("SourceEnqueuedAt = %v, want %v", rec.SourceEnqueuedAt, enqueuedAt)
	}

	laterNow := fixedNow.Add(time.Minute)
	store.now = func() time.Time { return laterNow }

	if err := store.MarkSynced(ctx, upn, enqueuedAt); err != nil {
		t.Fatalf("MarkSynced() error = %v", err)
	}
	rec, err = store.Get(ctx, upn)
	if err != nil {
		t.Fatalf("Get() after MarkSynced error = %v", err)
	}
	if rec.Status != StatusSynced {
		t.Errorf("Status after MarkSynced = %q, want %q", rec.Status, StatusSynced)
	}
	if !rec.UpdatedAt.Equal(laterNow) {
		t.Errorf("UpdatedAt = %v, want %v", rec.UpdatedAt, laterNow)
	}
}

func TestMemoryStoreIgnoresOutOfOrderCompletion(t *testing.T) {
	store := NewMemoryStore()
	upn := "jdoe@example.edu"
	ctx := context.Background()

	olderEnqueuedAt := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	newerEnqueuedAt := olderEnqueuedAt.Add(time.Minute)

	// The newer event (e.g. password_change) completes first...
	if err := store.MarkSynced(ctx, upn, newerEnqueuedAt); err != nil {
		t.Fatalf("MarkSynced() error = %v", err)
	}
	// ...then the older, slower event (e.g. a login_bootstrap retry) reports
	// failure. This stale write must be silently dropped, not applied.
	if err := store.MarkFailed(ctx, upn, olderEnqueuedAt); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}

	rec, err := store.Get(ctx, upn)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rec.Status != StatusSynced {
		t.Errorf("Status = %q, want %q (out-of-order failure must not overwrite newer success)", rec.Status, StatusSynced)
	}
	if !rec.SourceEnqueuedAt.Equal(newerEnqueuedAt) {
		t.Errorf("SourceEnqueuedAt = %v, want %v", rec.SourceEnqueuedAt, newerEnqueuedAt)
	}
}

func TestMemoryStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	upn := "user@example.edu"

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			enqueuedAt := time.Now()
			_ = store.MarkPending(ctx, upn, enqueuedAt)
			_, _ = store.Get(ctx, upn)
			_ = store.MarkSynced(ctx, upn, enqueuedAt)
		}(i)
	}
	wg.Wait()

	if rec, err := store.Get(ctx, upn); err != nil || rec.Status == StatusUnsynced {
		t.Fatalf("Get() after concurrent use = %+v, err=%v, want a recorded status", rec, err)
	}
}
