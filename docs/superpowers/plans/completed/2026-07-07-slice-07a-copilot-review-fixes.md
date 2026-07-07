# Slice 7A Copilot Review Fixes Implementation Plan

> **Plan Status:** Completed
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Resolve the two open Copilot review threads on PR #9 without changing Slice 7A's event/sync-status behavior.

**Architecture:** Keep the fixes local to `internal/migration.Service`: make invalid variadic options usage fail fast at construction time, and make `login_bootstrap` pending freshness fail open when a pending record timestamp is in the future relative to the service clock. Both changes are covered by focused service-level tests before implementation.

**Tech Stack:** Go 1.26 (per `go.mod`), standard library only (`testing`, `time`, `context`), existing `internal/migration` and `internal/syncstatus` packages.

---

## Review Threads

- Copilot thread `PRRT_kwDOTDvq586O1vkC`: `NewService` documents that more than one `ServiceOptions` is invalid, but the implementation silently ignores all options after the first.
- Copilot thread `PRRT_kwDOTDvq586O1vkX`: `skipLoginBootstrap` uses `s.now().Sub(rec.UpdatedAt) < s.pendingTTL`, so a pending record with an `UpdatedAt` value later than `s.now()` is treated as fresh because the duration is negative.

## File Structure

- Modify: `internal/migration/service.go` - fail fast on multiple options and update pending freshness logic.
- Modify: `internal/migration/service_test.go` - add focused regression tests for both review findings.

---

### Task 1: Fail Fast on Multiple ServiceOptions

**Files:**
- Modify: `internal/migration/service.go:74-82`
- Modify: `internal/migration/service_test.go`

**Rationale:** The current constructor comment says passing more than one `ServiceOptions` is invalid usage, but `NewService` only applies `opts[0]`. Failing fast keeps misconfiguration visible while preserving all existing call sites that pass zero or one option.

- [x] **Step 1: Write the failing test**

Append this test near the other `NewService`-level tests in `internal/migration/service_test.go`:

```go
func TestNewServicePanicsWithMultipleOptions(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewService did not panic with multiple ServiceOptions")
		}
	}()

	NewService(
		"nycu.edu.tw",
		&captureQueue{},
		&captureEncrypter{},
		ServiceOptions{PendingTTL: time.Minute},
		ServiceOptions{PendingTTL: 2 * time.Minute},
	)
}
```

- [x] **Step 2: Run the focused test to verify it fails**

Run:

```bash
go test ./internal/migration -run TestNewServicePanicsWithMultipleOptions -v
```

Expected: FAIL with `NewService did not panic with multiple ServiceOptions`.

- [x] **Step 3: Implement the fail-fast guard**

Change `NewService` in `internal/migration/service.go` to panic when callers pass more than one option:

```go
// NewService constructs a Service. opts is variadic so existing call sites
// keep compiling unchanged; passing more than one ServiceOptions is invalid
// usage and panics.
func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter, opts ...ServiceOptions) *Service {
	if len(opts) > 1 {
		panic("migration.NewService: at most one ServiceOptions is supported")
	}

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
```

- [x] **Step 4: Run the focused test to verify it passes**

Run:

```bash
go test ./internal/migration -run TestNewServicePanicsWithMultipleOptions -v
```

Expected: PASS.

- [x] **Step 5: Commit the task**

```bash
git add internal/migration/service.go internal/migration/service_test.go
git commit -m "fix(migration): reject multiple service options"
```

---

### Task 2: Fail Open on Future Pending Timestamps

**Files:**
- Modify: `internal/migration/service.go:171-183`
- Modify: `internal/migration/service_test.go`

**Rationale:** A pending record whose `UpdatedAt` is later than `s.now()` can happen after wall-clock rollback, VM resume, or tests that reconstruct timestamps without monotonic clock data. The service should not let that future timestamp suppress `login_bootstrap` enqueueing; the safe Slice 7A behavior is to fail open and enqueue rather than skip longer than intended.

- [x] **Step 1: Write the failing test**

Append this test after `TestServiceEnqueuesLoginBootstrapWhenPendingStale` in `internal/migration/service_test.go`:

```go
func TestServiceEnqueuesLoginBootstrapWhenPendingTimestampIsInFuture(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})
	fixedNow := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusPending, fixedNow.Add(10*time.Minute))

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (future pending timestamp should fail open)")
	}
	if decision.Skipped {
		t.Error("decision.Skipped = true, want false")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}
```

- [x] **Step 2: Run the focused test to verify it fails**

Run:

```bash
go test ./internal/migration -run TestServiceEnqueuesLoginBootstrapWhenPendingTimestampIsInFuture -v
```

Expected: FAIL because the existing `s.now().Sub(rec.UpdatedAt) < s.pendingTTL` check treats the negative duration as fresh and skips enqueueing.

- [x] **Step 3: Implement direct timestamp freshness with a future-timestamp guard**

Change the pending branch in `skipLoginBootstrap` in `internal/migration/service.go`:

```go
	case syncstatus.StatusPending:
		now := s.now()
		if rec.UpdatedAt.After(now) {
			return false, ""
		}
		if now.Before(rec.UpdatedAt.Add(s.pendingTTL)) {
			return true, "sync_pending"
		}
		return false, ""
```

This preserves the existing fresh-pending behavior for records updated within `PendingTTL`, preserves stale-pending re-enqueue behavior, and changes only the rollback/future-timestamp case to fail open.

- [x] **Step 4: Run the focused tests to verify pending behavior**

Run:

```bash
go test ./internal/migration -run 'TestService(SkipsLoginBootstrapWhenPendingFresh|EnqueuesLoginBootstrapWhenPendingStale|EnqueuesLoginBootstrapWhenPendingTimestampIsInFuture)' -v
```

Expected: PASS for all three tests.

- [x] **Step 5: Run package verification**

Run:

```bash
go test ./internal/migration
```

Expected: PASS.

- [x] **Step 6: Commit the task**

```bash
git add internal/migration/service.go internal/migration/service_test.go
git commit -m "fix(migration): fail open on future pending timestamps"
```

---

## Final Verification

- [x] Run `gofmt -w internal/migration/service.go internal/migration/service_test.go`.
- [x] Run `go test ./internal/migration`.
- [x] Run `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Push the branch and confirm both Copilot review threads are outdated or resolved.

## PR Reply Notes

Reply in each existing GitHub review thread, not as a top-level PR comment:

- `PRRT_kwDOTDvq586O1vkC`: mention that `NewService` now panics on multiple `ServiceOptions` and has `TestNewServicePanicsWithMultipleOptions`.
- `PRRT_kwDOTDvq586O1vkX`: mention that pending freshness now rejects future `UpdatedAt` values and has `TestServiceEnqueuesLoginBootstrapWhenPendingTimestampIsInFuture`.
