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

func TestMemoryStoreEqualTimestampPrecedenceAndIdempotency(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	upn := " User@Example.EDU "
	source := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	updated := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return updated }

	if err := store.MarkPending(ctx, upn, source); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return updated.Add(time.Minute) }
	if err := store.MarkPending(ctx, upn, source); err != nil {
		t.Fatal(err)
	}
	rec, _ := store.Get(ctx, "user@example.edu")
	if !rec.UpdatedAt.Equal(updated) {
		t.Fatalf("idempotent UpdatedAt = %v, want %v", rec.UpdatedAt, updated)
	}

	if err := store.MarkFailed(ctx, upn, source); err != nil {
		t.Fatal(err)
	}
	if rec, _ = store.Get(ctx, upn); rec.Status != StatusFailed {
		t.Fatalf("pending -> failed status = %q", rec.Status)
	}
	if err := store.MarkPending(ctx, upn, source); err != nil {
		t.Fatal(err)
	}
	if rec, _ = store.Get(ctx, upn); rec.Status != StatusFailed {
		t.Fatalf("failed -> pending status = %q, want failed", rec.Status)
	}
	if err := store.MarkSynced(ctx, upn, source); err != nil {
		t.Fatal(err)
	}
	if rec, _ = store.Get(ctx, upn); rec.Status != StatusSynced {
		t.Fatalf("failed -> synced status = %q", rec.Status)
	}
	for _, lower := range []Status{StatusPending, StatusFailed} {
		if err := store.set(ctx, upn, lower, source); err != nil {
			t.Fatal(err)
		}
	}
	if rec, _ = store.Get(ctx, upn); rec.Status != StatusSynced {
		t.Fatalf("synced downgraded to %q", rec.Status)
	}
}

func TestMemoryStoreAllEqualTimestampTransitions(t *testing.T) {
	t.Parallel()

	source := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	statuses := []Status{StatusPending, StatusFailed, StatusSynced}
	for _, first := range statuses {
		for _, next := range statuses {
			first, next := first, next
			t.Run(string(first)+"_to_"+string(next), func(t *testing.T) {
				t.Parallel()
				store := NewMemoryStore()
				if err := store.set(context.Background(), "user@example.edu", first, source); err != nil {
					t.Fatal(err)
				}
				if err := store.set(context.Background(), "user@example.edu", next, source); err != nil {
					t.Fatal(err)
				}
				rec, err := store.Get(context.Background(), "user@example.edu")
				if err != nil {
					t.Fatal(err)
				}
				want := first
				if statusPrecedence(next) > statusPrecedence(first) {
					want = next
				}
				if rec.Status != want {
					t.Fatalf("status = %q, want %q", rec.Status, want)
				}
			})
		}
	}
}

func TestMemoryStorePropagatesCanceledContext(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "user@example.edu"); err != context.Canceled {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
	if err := store.MarkPending(ctx, "user@example.edu", time.Now()); err != context.Canceled {
		t.Fatalf("MarkPending error = %v, want context.Canceled", err)
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
