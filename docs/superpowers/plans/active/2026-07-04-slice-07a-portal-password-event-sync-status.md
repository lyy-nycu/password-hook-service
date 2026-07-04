# Slice 7A: Portal Password Event Semantics and Sync Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the hook explicit about *why* it was called (`eventType`), and stop redundantly re-syncing already-synced accounts on every `login_bootstrap` call while still always syncing real password changes.

**Architecture:** The portal will send a new required `eventType` field (`login_bootstrap` | `password_change` | `password_recovery`) on every hook request. The `migration.Service` gains an in-process `syncstatus.Store` that records per-UPN sync state (`unsynced` / `sync_pending` / `synced` / `sync_failed`). `login_bootstrap` events consult this store and skip enqueueing when the UPN is already `synced` or has a fresh `sync_pending` entry (still within a TTL window); `password_change` and `password_recovery` events always enqueue regardless of stored state. The worker updates the store to `synced`/`sync_failed` after processing each message, closing the feedback loop.

**Tech Stack:** Go 1.26 (per `go.mod`), standard library only for the new code (`sync`, `time`, `context`, `errors`, `encoding/json`), existing `internal/migration`, `internal/handler`, `internal/worker`, `internal/app` packages.

## Global Constraints

- Go 1.26 (see `go.mod`).
- Passwords must never be logged or persisted in plaintext; `ZeroBytes` must still be called on every `Submit` invocation (see `internal/migration/service.go`).
- The hook's external HTTP contract must keep returning `202 Accepted` semantics for all successful submissions, including cases where the service internally skips enqueueing (`login_bootstrap` dedupe) — the caller never observes a different status code for a skip vs. an actual enqueue.
- No new third-party dependencies. Use only what's already imported across the repo.
- Follow existing repo conventions: `context.Context` is always the first parameter; table-style Go tests with descriptive `Test<Subject><Behavior>` names; fakes/test-doubles are unexported structs local to `_test.go` files; TDD step order is "write failing test → confirm it fails to compile/run → implement → confirm it passes → commit".

---

## Scope

**In scope:**
- Add `EventType` to the wire message and hook request/response contract.
- Add an in-process, non-durable `internal/syncstatus` package tracking per-UPN sync state.
- Change `migration.Service.Submit` to skip enqueueing `login_bootstrap` events when the UPN is already synced or has a fresh pending sync in flight.
- Always enqueue `password_change` and `password_recovery` events regardless of sync state.
- Update the worker to mark sync status `synced`/`sync_failed` after processing.
- Wire the new store into `internal/app` for both the HTTP-only and full-worker app assembly paths.
- Update `README.md` and `docs/examples/sign-hook-request.php` to document the new field and behavior.

**Out of scope (deferred to Slice 10 - Infrastructure):**
- Durable/shared storage for sync status (e.g., Redis, database) — this slice uses an in-memory store that resets on process restart and does not scale across multiple replicas.
- Any new configuration knobs for the pending-sync TTL (hardcoded constant in this slice).
- Any change to encryption, HMAC signing, or nonce replay protection (Slice 7 already covers these).

## Current Context

Slice 7 (password data protection: AES-GCM encryption, HMAC request signing, replay protection) is complete and merged (`docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md`). The design spec (`docs/superpowers/specs/2026-06-24-password-hook-service-design.md`) has already been amended in this working tree (§1.2.1 Amendment section, updated API contract table with `eventType`, updated Message TTL Expiry section, updated §11.3 PHP example) to correct an earlier false assumption that the hook fires "on every successful login" — it actually fires on `login_bootstrap` (post-SSO bootstrap), `password_change`, and `password_recovery` events from the portal. This plan implements the code changes that make that corrected model real.

The originating draft is `docs/superpowers/plans/drafts/2026-07-03-portal-password-event-sync-story.md`; its "Acceptance Criteria For Promotion" section is fully covered by Task 4's nine new service-level tests (in particular: resync-after-`sync_failed`, and always-resync for `password_recovery` even when already synced).

## File Structure

- Create: `internal/migration/event.go` — `EventType` type, event constants, validation.
- Create: `internal/migration/event_test.go` — validation tests.
- Modify: `internal/migration/message.go` — add `EventType` field to `PasswordSyncMessage`.
- Create: `internal/migration/message_test.go` — round-trip and backward-compat decode tests.
- Create: `internal/syncstatus/status.go` — `Status` type, `Record`, `Store` interface, `MemoryStore` implementation.
- Create: `internal/syncstatus/status_test.go` — `MemoryStore` behavior tests.
- Modify: `internal/migration/service.go` — `ServiceOptions`, `SyncStatusStore` interface, dedupe logic in `Submit`.
- Modify: `internal/migration/service_test.go` — update 5 existing `NewService` call sites, add 9 new tests + `fakeSyncStatusStore`.
- Modify: `internal/handler/hook.go` — `EventType` field + validation + wiring into `migration.Request`.
- Modify: `internal/handler/hook_test.go` — update 9 `NewService` call sites, add `eventType` to 5 JSON bodies, add 2 new tests.
- Modify: `internal/worker/worker.go` — `SyncStatusRecorder` interface, `Options` field, `processMessage` hooks.
- Modify: `internal/worker/worker_test.go` — `fakeSyncStatusRecorder`, update helpers, rewrite `TestNewValidatesDependencies`, add 3 new tests.
- Modify: `internal/app/app.go` — wire `syncstatus.MemoryStore` into both app-assembly paths.
- Modify: `internal/app/app_test.go` — update 2 `newWithQueue` call sites, add `eventType` to 4 JSON bodies.
- Modify: `README.md` — update example request body, add "Event Types and Sync Status" section.
- Modify: `docs/examples/sign-hook-request.php` — add `eventType` to the example payload.

---

### Task 1: Event type definitions

**Files:**
- Create: `internal/migration/event.go`
- Create: `internal/migration/event_test.go`

**Interfaces:**
- Consumes: nothing (leaf package-local type).
- Produces: `type EventType string`; constants `EventLoginBootstrap`, `EventPasswordChange`, `EventPasswordRecovery`; `var ErrInvalidEventType error`; `func ValidEventType(t EventType) bool`. Task 2 embeds `EventType` in `PasswordSyncMessage`. Task 4 validates it in `Submit` and returns `ErrInvalidEventType`. Task 5 validates it in the hook handler.

- [ ] **Step 1: Write the failing test**

Create `internal/migration/event_test.go`:

```go
package migration

import "testing"

func TestValidEventTypeAcceptsKnownEvents(t *testing.T) {
	for _, et := range []EventType{EventLoginBootstrap, EventPasswordChange, EventPasswordRecovery} {
		if !ValidEventType(et) {
			t.Errorf("ValidEventType(%q) = false, want true", et)
		}
	}
}

func TestValidEventTypeRejectsUnknownEvent(t *testing.T) {
	if ValidEventType(EventType("password_reset")) {
		t.Error("ValidEventType(\"password_reset\") = true, want false")
	}
	if ValidEventType(EventType("")) {
		t.Error("ValidEventType(\"\") = true, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/migration/... -run TestValidEventType -v`
Expected: FAIL — build error, `EventType`/`ValidEventType`/constants undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/migration/event.go`:

```go
package migration

import "errors"

// EventType identifies why the portal invoked the password hook. See design
// spec §1.2.1 Amendment for the corrected event model: the hook is not
// invoked on every successful login, but on these three distinct portal
// events.
type EventType string

const (
	// EventLoginBootstrap fires when a user completes SSO login and the
	// portal bootstraps their on-prem AD account. Submit skips enqueueing
	// this event when the UPN is already synced or has a fresh pending sync.
	EventLoginBootstrap EventType = "login_bootstrap"
	// EventPasswordChange fires when a user changes their password. Always
	// enqueued regardless of prior sync status.
	EventPasswordChange EventType = "password_change"
	// EventPasswordRecovery fires when a user recovers/resets their
	// password. Always enqueued regardless of prior sync status.
	EventPasswordRecovery EventType = "password_recovery"
)

// ErrInvalidEventType is returned by Service.Submit when the request's
// EventType is empty or not one of the known constants.
var ErrInvalidEventType = errors.New("migration: invalid event type")

// ValidEventType reports whether t is one of the known EventType constants.
func ValidEventType(t EventType) bool {
	switch t {
	case EventLoginBootstrap, EventPasswordChange, EventPasswordRecovery:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/migration/... -run TestValidEventType -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/migration/event.go internal/migration/event_test.go
git commit -m "feat(migration): add EventType with validation"
```

---

### Task 2: Add EventType to the wire message

**Files:**
- Modify: `internal/migration/message.go`
- Create: `internal/migration/message_test.go`

**Interfaces:**
- Consumes: `EventType` from Task 1.
- Produces: `PasswordSyncMessage.EventType EventType` field with JSON tag `"eventType"`. Task 4's `Submit` sets this field when building the message; Task 6's worker reads `passwordSyncMessage.EventType` is NOT required (worker only needs `.UPN`), but the field must round-trip correctly through the queue for forward compatibility.

- [ ] **Step 1: Write the failing test**

Create `internal/migration/message_test.go`:

```go
package migration

import (
	"encoding/json"
	"testing"
)

func TestPasswordSyncMessageRoundTripsEventType(t *testing.T) {
	msg := PasswordSyncMessage{
		CN:        "jdoe",
		UPN:       "jdoe@example.edu",
		EventType: EventLoginBootstrap,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded PasswordSyncMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EventType != EventLoginBootstrap {
		t.Errorf("decoded.EventType = %q, want %q", decoded.EventType, EventLoginBootstrap)
	}
}

func TestPasswordSyncMessageDecodesWithoutEventType(t *testing.T) {
	// Legacy messages already sitting in the queue at deploy time won't have
	// an eventType field. Decoding must not error, and EventType must be the
	// empty string rather than some invalid sentinel.
	legacyJSON := `{"cn":"jdoe","upn":"jdoe@example.edu"}`

	var decoded PasswordSyncMessage
	if err := json.Unmarshal([]byte(legacyJSON), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EventType != EventType("") {
		t.Errorf("decoded.EventType = %q, want empty string", decoded.EventType)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/migration/... -run TestPasswordSyncMessage -v`
Expected: FAIL — `decoded.EventType` undefined (field doesn't exist yet).

- [ ] **Step 3: Write minimal implementation**

Modify `internal/migration/message.go` — add the `EventType` field to the `PasswordSyncMessage` struct, inserted after `UPN` and before `Password`:

```go
type PasswordSyncMessage struct {
	CN                 string    `json:"cn"`
	UPN                string    `json:"upn"`
	EventType          EventType `json:"eventType"`
	Password           string    `json:"password,omitempty"`
	PasswordCiphertext string    `json:"passwordCiphertext,omitempty"`
	PasswordNonce      string    `json:"passwordNonce,omitempty"`
	PasswordKeyID      string    `json:"passwordKeyId,omitempty"`
	PasswordAlg        string    `json:"passwordAlg,omitempty"`
	DisplayName        string    `json:"displayName,omitempty"`
	Mail               string    `json:"mail,omitempty"`
	EnqueuedAt         time.Time `json:"enqueuedAt"`
}
```

(Only the new `EventType` line is added; all other fields and tags stay exactly as they are in the current file — verify with `git diff` after editing that no other field was altered.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/migration/... -run TestPasswordSyncMessage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/migration/message.go internal/migration/message_test.go
git commit -m "feat(migration): add EventType field to PasswordSyncMessage"
```

---

### Task 3: In-process sync status store

**Files:**
- Create: `internal/syncstatus/status.go`
- Create: `internal/syncstatus/status_test.go`

**Interfaces:**
- Consumes: nothing (standalone package; `context`, `sync`, `time` stdlib only).
- Produces: `type Status string` with constants `StatusUnsynced`, `StatusSyncPending`, `StatusSynced`, `StatusSyncFailed`; `type Record struct { Status Status; UpdatedAt time.Time }`; `type Store interface { Get(ctx context.Context, upn string) (Record, bool, error); MarkPending(ctx context.Context, upn string) error; MarkSynced(ctx context.Context, upn string) error; MarkFailed(ctx context.Context, upn string) error }`; `type MemoryStore struct{...}` (unexported fields) implementing `Store`; `func NewMemoryStore() *MemoryStore`. Task 4 consumes `Store.Get`/`MarkPending` via a package-local narrower interface. Task 6 consumes `MarkSynced`/`MarkFailed` via a package-local narrower interface. Task 7 constructs `*MemoryStore` via `NewMemoryStore()` and passes the same instance to both.

- [ ] **Step 1: Write the failing test**

Create `internal/syncstatus/status_test.go`:

```go
package syncstatus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreGetReturnsNotFoundInitially(t *testing.T) {
	store := NewMemoryStore()

	_, found, err := store.Get(context.Background(), "jdoe@example.edu")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Error("Get() found = true on empty store, want false")
	}
}

func TestMemoryStoreTracksLifecycle(t *testing.T) {
	store := NewMemoryStore()
	upn := "jdoe@example.edu"

	fixedNow := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }

	ctx := context.Background()

	if err := store.MarkPending(ctx, upn); err != nil {
		t.Fatalf("MarkPending() error = %v", err)
	}
	rec, found, err := store.Get(ctx, upn)
	if err != nil || !found {
		t.Fatalf("Get() after MarkPending: found=%v err=%v, want found=true err=nil", found, err)
	}
	if rec.Status != StatusSyncPending {
		t.Errorf("Status after MarkPending = %q, want %q", rec.Status, StatusSyncPending)
	}
	if !rec.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", rec.UpdatedAt, fixedNow)
	}

	laterNow := fixedNow.Add(time.Minute)
	store.now = func() time.Time { return laterNow }

	if err := store.MarkSynced(ctx, upn); err != nil {
		t.Fatalf("MarkSynced() error = %v", err)
	}
	rec, found, err = store.Get(ctx, upn)
	if err != nil || !found {
		t.Fatalf("Get() after MarkSynced: found=%v err=%v", found, err)
	}
	if rec.Status != StatusSynced {
		t.Errorf("Status after MarkSynced = %q, want %q", rec.Status, StatusSynced)
	}
	if !rec.UpdatedAt.Equal(laterNow) {
		t.Errorf("UpdatedAt = %v, want %v", rec.UpdatedAt, laterNow)
	}

	evenLaterNow := laterNow.Add(time.Minute)
	store.now = func() time.Time { return evenLaterNow }

	if err := store.MarkFailed(ctx, upn); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	rec, found, err = store.Get(ctx, upn)
	if err != nil || !found {
		t.Fatalf("Get() after MarkFailed: found=%v err=%v", found, err)
	}
	if rec.Status != StatusSyncFailed {
		t.Errorf("Status after MarkFailed = %q, want %q", rec.Status, StatusSyncFailed)
	}
}

func TestMemoryStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			upn := "user@example.edu"
			_ = store.MarkPending(ctx, upn)
			_, _, _ = store.Get(ctx, upn)
			_ = store.MarkSynced(ctx, upn)
		}(i)
	}
	wg.Wait()

	if _, found, err := store.Get(ctx, "user@example.edu"); err != nil || !found {
		t.Fatalf("Get() after concurrent use: found=%v err=%v", found, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/syncstatus/... -v`
Expected: FAIL — build error, package `internal/syncstatus` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `internal/syncstatus/status.go`:

```go
// Package syncstatus tracks the in-process sync state of each portal
// identity's password sync, so that repeated login_bootstrap events don't
// redundantly re-enqueue an already-synced account. See design spec
// §1.2.1 Amendment.
//
// MemoryStore is intentionally non-durable: it resets on process restart
// and is not shared across replicas. Slice 10 (infrastructure) replaces
// this with a durable, shared store.
package syncstatus

import (
	"context"
	"sync"
	"time"
)

// Status represents the last known sync outcome for a UPN.
type Status string

const (
	// StatusUnsynced means no sync has ever been attempted (or the store
	// has no record — Get's second return value distinguishes these, but
	// callers that already know a record exists can use this value too).
	StatusUnsynced Status = "unsynced"
	// StatusSyncPending means a sync message was enqueued and the worker
	// has not yet reported an outcome.
	StatusSyncPending Status = "sync_pending"
	// StatusSynced means the worker successfully processed the sync.
	StatusSynced Status = "synced"
	// StatusSyncFailed means the worker exhausted retries or hit a
	// permanent error while processing the sync.
	StatusSyncFailed Status = "sync_failed"
)

// Record is a snapshot of a UPN's sync status at a point in time.
type Record struct {
	Status    Status
	UpdatedAt time.Time
}

// Store tracks per-UPN sync status. Implementations must be safe for
// concurrent use.
type Store interface {
	// Get returns the current record for upn. found is false if no record
	// exists yet.
	Get(ctx context.Context, upn string) (Record, bool, error)
	// MarkPending records that a sync message was just enqueued for upn.
	MarkPending(ctx context.Context, upn string) error
	// MarkSynced records that upn was successfully synced.
	MarkSynced(ctx context.Context, upn string) error
	// MarkFailed records that syncing upn failed permanently or after
	// exhausting retries.
	MarkFailed(ctx context.Context, upn string) error
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

func (s *MemoryStore) Get(_ context.Context, upn string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, found := s.records[upn]
	return rec, found, nil
}

func (s *MemoryStore) MarkPending(ctx context.Context, upn string) error {
	return s.set(ctx, upn, StatusSyncPending)
}

func (s *MemoryStore) MarkSynced(ctx context.Context, upn string) error {
	return s.set(ctx, upn, StatusSynced)
}

func (s *MemoryStore) MarkFailed(ctx context.Context, upn string) error {
	return s.set(ctx, upn, StatusSyncFailed)
}

func (s *MemoryStore) set(_ context.Context, upn string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[upn] = Record{Status: status, UpdatedAt: s.now()}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/syncstatus/... -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/syncstatus/status.go internal/syncstatus/status_test.go
git commit -m "feat(syncstatus): add in-process sync status store"
```

---

### Task 4: Service dedupe logic

**Files:**
- Modify: `internal/migration/service.go`
- Modify: `internal/migration/service_test.go`

**Interfaces:**
- Consumes: `EventType`/`ValidEventType`/`ErrInvalidEventType` from Task 1; `syncstatus.Record`, `syncstatus.Status` constants from Task 3 (imports `github.com/nycu/password-hook-service/internal/syncstatus` — confirm exact module path via `go.mod` before writing the import).
- Produces: `type ServiceOptions struct { SyncStatusStore SyncStatusStore; PendingTTL time.Duration }`; `type SyncStatusStore interface { Get(ctx context.Context, upn string) (syncstatus.Record, bool, error); MarkPending(ctx context.Context, upn string) error }`; `NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter, opts ServiceOptions) *Service` (signature change: 4th parameter added); `Request.EventType EventType` field. Task 5 (hook.go) calls `migration.NewService(..., migration.ServiceOptions{...})` and sets `migration.Request{EventType: ...}`. Task 7 (app.go) constructs `migration.ServiceOptions{SyncStatusStore: syncStatusStore}` where `syncStatusStore` is `*syncstatus.MemoryStore` (structurally satisfies `SyncStatusStore`).

- [ ] **Step 1: Confirm the module path**

Run: `head -1 go.mod`
Expected output: `module github.com/nycu/password-hook-service` (or record the actual value and substitute it in the import path below if different).

- [ ] **Step 2: Write the failing tests**

Add to `internal/migration/service_test.go` — first, a `fakeSyncStatusStore` test double (place near existing fakes in the file):

```go
type fakeSyncStatusStore struct {
	mu          sync.Mutex
	records     map[string]syncstatus.Record
	getErr      error
	markErr     error
	markPending []string
}

func newFakeSyncStatusStore() *fakeSyncStatusStore {
	return &fakeSyncStatusStore{records: make(map[string]syncstatus.Record)}
}

func (f *fakeSyncStatusStore) Get(_ context.Context, upn string) (syncstatus.Record, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return syncstatus.Record{}, false, f.getErr
	}
	rec, found := f.records[upn]
	return rec, found, nil
}

func (f *fakeSyncStatusStore) MarkPending(_ context.Context, upn string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markPending = append(f.markPending, upn)
	if f.markErr != nil {
		return f.markErr
	}
	f.records[upn] = syncstatus.Record{Status: syncstatus.StatusSyncPending, UpdatedAt: time.Now()}
	return nil
}

func (f *fakeSyncStatusStore) setRecord(upn string, status syncstatus.Status, updatedAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[upn] = syncstatus.Record{Status: status, UpdatedAt: updatedAt}
}
```

Then add these 9 new test functions to `internal/migration/service_test.go`:

```go
func TestServiceRejectsInvalidEventType(t *testing.T) {
	queue := &fakeQueue{}
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{})

	_, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventType("bogus"),
	})

	if !errors.Is(err, ErrInvalidEventType) {
		t.Fatalf("Submit() error = %v, want ErrInvalidEventType", err)
	}
	if len(queue.enqueued) != 0 {
		t.Errorf("queue.enqueued = %d messages, want 0", len(queue.enqueued))
	}
}

func TestServiceSkipsLoginBootstrapWhenAlreadySynced(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued == false {
		// decision.Enqueued must be false for a skip; assert directly below.
	}
	if decision.Enqueued {
		t.Error("decision.Enqueued = true, want false (should skip already-synced login_bootstrap)")
	}
	if len(queue.enqueued) != 0 {
		t.Errorf("queue.enqueued = %d messages, want 0", len(queue.enqueued))
	}
}

func TestServiceSkipsLoginBootstrapWhenPendingFresh(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSyncPending, time.Now().Add(-1*time.Minute))

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if decision.Enqueued {
		t.Error("decision.Enqueued = true, want false (fresh pending sync should skip)")
	}
	if len(queue.enqueued) != 0 {
		t.Errorf("queue.enqueued = %d messages, want 0", len(queue.enqueued))
	}
}

func TestServiceEnqueuesLoginBootstrapWhenPendingStale(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSyncPending, time.Now().Add(-10*time.Minute))

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (stale pending sync should re-enqueue)")
	}
	if len(queue.enqueued) != 1 {
		t.Errorf("queue.enqueued = %d messages, want 1", len(queue.enqueued))
	}
}

func TestServiceEnqueuesLoginBootstrapAfterSyncFailed(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSyncFailed, time.Now())

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (sync_failed should re-enqueue on next login_bootstrap)")
	}
	if len(queue.enqueued) != 1 {
		t.Errorf("queue.enqueued = %d messages, want 1", len(queue.enqueued))
	}
}

func TestServiceAlwaysEnqueuesPasswordChangeEvenWhenSynced(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("newsecret"),
		EventType: EventPasswordChange,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_change always resyncs)")
	}
	if len(queue.enqueued) != 1 {
		t.Errorf("queue.enqueued = %d messages, want 1", len(queue.enqueued))
	}
}

func TestServiceAlwaysEnqueuesPasswordRecoveryEvenWhenSynced(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "jdoe@example.edu"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("recoveredsecret"),
		EventType: EventPasswordRecovery,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_recovery always resyncs)")
	}
	if len(queue.enqueued) != 1 {
		t.Errorf("queue.enqueued = %d messages, want 1", len(queue.enqueued))
	}
}

func TestServiceMarksPendingAfterSuccessfulEnqueue(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	_, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if len(store.markPending) != 1 || store.markPending[0] != "jdoe@example.edu" {
		t.Errorf("store.markPending = %v, want [\"jdoe@example.edu\"]", store.markPending)
	}
}

func TestServiceSyncStatusStoreErrorFailsOpen(t *testing.T) {
	queue := &fakeQueue{}
	store := newFakeSyncStatusStore()
	store.getErr = errors.New("store unavailable")
	svc := NewService("example.edu", queue, &fakeEncrypter{}, ServiceOptions{SyncStatusStore: store})

	decision, err := svc.Submit(context.Background(), Request{
		CN:        "jdoe",
		Password:  []byte("secret"),
		EventType: EventLoginBootstrap,
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil (store errors must fail open)", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (store Get error must not block enqueue)")
	}
	if len(queue.enqueued) != 1 {
		t.Errorf("queue.enqueued = %d messages, want 1", len(queue.enqueued))
	}
}
```

Add `"sync"` and `"github.com/nycu/password-hook-service/internal/syncstatus"` to the test file's import block if not already present (adjust the module path per Step 1's confirmed value).

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/migration/... -v`
Expected: FAIL — build error. The new tests call `NewService` with a 4th `ServiceOptions{}` argument, but the current 3-argument `NewService` signature doesn't accept it, and `Request.EventType`/`ServiceOptions`/`SyncStatusStore` don't exist yet.

- [ ] **Step 4: Rewrite service.go**

Modify `internal/migration/service.go`. Add the import (adjust module path per Step 1):

```go
import (
	// ...existing imports...
	"github.com/nycu/password-hook-service/internal/syncstatus"
)
```

Add near the top of the file, after existing const/var declarations:

```go
// defaultPendingTTL bounds how long a sync_pending record suppresses a
// repeated login_bootstrap enqueue before being treated as stale (e.g. the
// worker crashed before reporting an outcome). Matches the queue message's
// own default TTL (see config.PasswordMessageTTL's 300s default) so a
// pending record never outlives the message it corresponds to.
const defaultPendingTTL = 300 * time.Second

// SyncStatusStore is the narrow view of syncstatus.Store that Service
// needs. *syncstatus.MemoryStore satisfies this interface.
type SyncStatusStore interface {
	Get(ctx context.Context, upn string) (syncstatus.Record, bool, error)
	MarkPending(ctx context.Context, upn string) error
}

// ServiceOptions configures optional Service behavior.
type ServiceOptions struct {
	// SyncStatusStore, if non-nil, enables login_bootstrap dedupe. If nil,
	// every login_bootstrap event is enqueued unconditionally (dedupe
	// disabled).
	SyncStatusStore SyncStatusStore
	// PendingTTL bounds how long a sync_pending record suppresses a repeat
	// login_bootstrap enqueue. Defaults to defaultPendingTTL when <= 0.
	PendingTTL time.Duration
}
```

Add `EventType EventType` to the `Request` struct (place it next to the other request fields, e.g. after `CN`):

```go
type Request struct {
	CN        string
	EventType EventType
	Password  []byte
	// ...existing fields unchanged...
}
```

Add `syncStatusStore SyncStatusStore` and `pendingTTL time.Duration` fields to the `Service` struct.

Change `NewService`'s signature and body:

```go
func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter, opts ServiceOptions) *Service {
	pendingTTL := opts.PendingTTL
	if pendingTTL <= 0 {
		pendingTTL = defaultPendingTTL
	}
	return &Service{
		primaryDomain:   primaryDomain,
		queue:           queue,
		encrypter:       encrypter,
		syncStatusStore: opts.SyncStatusStore,
		pendingTTL:      pendingTTL,
	}
}
```

(Keep all other existing fields in the returned `&Service{...}` literal exactly as they currently are — only add the two new ones.)

In `Submit`, immediately after `defer ZeroBytes(req.Password)`, add event type validation as the very first check:

```go
func (s *Service) Submit(ctx context.Context, req Request) (Decision, error) {
	defer ZeroBytes(req.Password)

	if !ValidEventType(req.EventType) {
		return Decision{}, ErrInvalidEventType
	}

	// ...existing ClassifyCN / external-email / unknown-identity / BuildUPN logic unchanged...
```

After the existing `BuildUPN` call succeeds (i.e., once `upn` is known) and before the existing queue/encrypter nil checks, add the dedupe check:

```go
	if req.EventType == EventLoginBootstrap && s.syncStatusStore != nil {
		if skip, _ := s.skipLoginBootstrap(ctx, upn); skip {
			return Decision{Enqueued: false}, nil
		}
	}

	// ...existing queue/encrypter nil checks, message building, encrypt, enqueue...
```

Add the `EventType` field to the message-building literal (wherever `PasswordSyncMessage{...}` is constructed in `Submit`):

```go
	msg := PasswordSyncMessage{
		CN:        cn,
		UPN:       upn,
		EventType: req.EventType,
		// ...existing fields unchanged...
	}
```

After a successful enqueue (right before `Submit`'s final `return Decision{Enqueued: true}, nil` or equivalent success path), add the best-effort pending mark:

```go
	if s.syncStatusStore != nil {
		_ = s.syncStatusStore.MarkPending(ctx, upn)
	}

	return Decision{Enqueued: true}, nil
```

Add the new helper method at the end of the file:

```go
// skipLoginBootstrap reports whether a login_bootstrap event for upn should
// be skipped because the account is already synced or has a fresh pending
// sync in flight. Store errors are treated as "no record" (fail-open): a
// sync-status outage must never block logins from bootstrapping.
func (s *Service) skipLoginBootstrap(ctx context.Context, upn string) (bool, string) {
	rec, found, err := s.syncStatusStore.Get(ctx, upn)
	if err != nil || !found {
		return false, ""
	}
	switch rec.Status {
	case syncstatus.StatusSynced:
		return true, "already_synced"
	case syncstatus.StatusSyncPending:
		if time.Since(rec.UpdatedAt) < s.pendingTTL {
			return true, "sync_pending"
		}
		return false, ""
	default:
		return false, ""
	}
}
```

- [ ] **Step 5: Run tests to verify the new failures shift**

Run: `go test ./internal/migration/... -v`
Expected: FAIL — the 9 new tests should now build and mostly pass, but the 5 pre-existing `NewService(...)` call sites (3-argument form) in `service_test.go` now fail to compile, since `NewService` requires 4 arguments.

- [ ] **Step 6: Update the 5 pre-existing call sites**

In `internal/migration/service_test.go`, update each of the 5 existing `NewService(primaryDomain, queue, encrypter)` calls (originally at lines 24, 63, 88, 114, 135) to pass a 4th argument, `ServiceOptions{}`:

```go
svc := NewService("example.edu", queue, encrypter, ServiceOptions{})
```

And add `EventType: EventLoginBootstrap` to each of the corresponding 5 `Request{...}` literals (originally at lines 27, 66, 90, 116, 138) so the existing tests keep exercising valid requests:

```go
req := Request{
	CN:        "jdoe",
	EventType: EventLoginBootstrap,
	Password:  []byte("secret"),
	// ...existing fields unchanged...
}
```

- [ ] **Step 7: Run all migration tests to verify they pass**

Run: `go test ./internal/migration/... -v`
Expected: PASS (all tests, old and new)

- [ ] **Step 8: Commit**

```bash
git add internal/migration/service.go internal/migration/service_test.go
git commit -m "feat(migration): dedupe login_bootstrap syncs via syncstatus store"
```

---

### Task 5: Hook handler validation and wiring

**Files:**
- Modify: `internal/handler/hook.go`
- Modify: `internal/handler/hook_test.go`

**Interfaces:**
- Consumes: `migration.EventType`, `migration.ValidEventType`, `migration.ErrInvalidEventType`, `migration.Request.EventType`, `migration.ServiceOptions` (all from Task 4).
- Produces: `passwordHookRequest.EventType migration.EventType` (JSON tag `eventType`). No new exported functions — this task only changes request validation and the `migration.Request{}` literal inside the existing `ServeHTTP` handler.

- [ ] **Step 1: Write the failing tests**

Add to `internal/handler/hook_test.go`:

```go
func TestHookRejectsMissingEventType(t *testing.T) {
	body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu"}`)
	req := newSignedHookRequest(t, body)
	rec := httptest.NewRecorder()

	handler := newTestHookHandler(t)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "eventType") {
		t.Errorf("body = %q, want mention of eventType", rec.Body.String())
	}
}

func TestHookRejectsInvalidEventType(t *testing.T) {
	body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu","eventType":"password_reset"}`)
	req := newSignedHookRequest(t, body)
	rec := httptest.NewRecorder()

	handler := newTestHookHandler(t)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "eventType") {
		t.Errorf("body = %q, want mention of eventType", rec.Body.String())
	}
}
```

Note: adjust `newSignedHookRequest`/`newTestHookHandler` to the actual helper names already used elsewhere in `hook_test.go` (re-check the file for the exact existing helper names and call conventions before pasting — the two new tests must follow the same construction pattern as neighboring tests like `TestHookRejectsUnknownCNAsBadRequest`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/handler/... -run TestHookRejects -v`
Expected: FAIL — `TestHookRejectsMissingEventType` and `TestHookRejectsInvalidEventType` fail because the handler doesn't yet validate `eventType` (missing/invalid eventType bodies currently succeed or fail for unrelated reasons).

- [ ] **Step 3: Update hook.go**

Modify `internal/handler/hook.go`. Add `EventType` to the `passwordHookRequest` struct:

```go
type passwordHookRequest struct {
	CN          string              `json:"cn"`
	EventType   migration.EventType `json:"eventType"`
	Password    string              `json:"password"`
	DisplayName string              `json:"displayName"`
	Mail        string              `json:"mail"`
}
```

In the `validate()` method's switch statement, append two new cases at the end (after the existing cn/password/displayName/mail checks), before the `default: return nil` (or equivalent terminal case):

```go
	case strings.TrimSpace(string(r.EventType)) == "":
		return &problemDetail{Title: "Invalid request", Detail: "Field 'eventType' is required"}
	case !migration.ValidEventType(r.EventType):
		return &problemDetail{Title: "Invalid request", Detail: "Field 'eventType' must be one of login_bootstrap, password_change, password_recovery"}
```

(Match the exact existing `problemDetail`/return-type shape already used by the other cases in this switch — copy the surrounding case's error-construction style verbatim rather than introducing a new pattern.)

In `ServeHTTP`, add `EventType: body.EventType` to the `migration.Request{...}` literal:

```go
	req := migration.Request{
		CN:        body.CN,
		EventType: body.EventType,
		Password:  []byte(body.Password),
		// ...existing fields unchanged...
	}
```

In the error-handling `errors.Is` chain that maps `Submit` errors to HTTP status codes, add the new sentinel:

```go
	if errors.Is(err, migration.ErrExternalIdentity) || errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrInvalidEventType) {
		// existing 400 handling unchanged
	}
```

(Match this to the exact existing chain structure in the file — the goal is just to route `ErrInvalidEventType` to the same 400-response path as the other validation sentinels.)

- [ ] **Step 4: Update the 9 existing NewService call sites**

In `internal/handler/hook_test.go`, each of the 9 `migration.NewService(primaryDomain, queue, encrypter)` calls (originally at lines 23, 48, 88, 185, 204, 263, 284, 302, 319) needs a 4th argument appended:

```go
svc := migration.NewService(primaryDomain, queue, encrypter, migration.ServiceOptions{})
```

- [ ] **Step 5: Add eventType to the 5 JSON body fixtures that must keep succeeding**

In `internal/handler/hook_test.go`, add `"eventType":"login_bootstrap"` to the JSON body strings in these 5 tests (originally at lines 26, 51, 266, 287, 305) so they continue to reach and pass the full validation chain:

- `TestHookEnqueuesInternalStudentID` (line 26)
- `TestHookZerosDecodedPasswordAfterSubmit` (line 51)
- `TestHookSkipsExternalEmailIdentity` (line 266)
- `TestHookRejectsUnknownCNAsBadRequest` (line 287)
- `TestHookQueueFailureReturnsInternalError` (line 305)

Example transformation:

```go
// before
body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu"}`)
// after
body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu","eventType":"login_bootstrap"}`)
```

Do not modify the other JSON bodies in the file (lines 73, 92, 120, 135, 189, 207, 322 and similar) — those tests fail at an earlier validation step or bypass `validate()` entirely, so they don't need `eventType` to keep their current expected outcome.

- [ ] **Step 6: Run all handler tests to verify they pass**

Run: `go test ./internal/handler/... -v`
Expected: PASS (all tests, old and new)

- [ ] **Step 7: Commit**

```bash
git add internal/handler/hook.go internal/handler/hook_test.go
git commit -m "feat(handler): validate and forward eventType on password hook"
```

---

### Task 6: Worker sync status recording

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

**Interfaces:**
- Consumes: nothing new from prior tasks (only needs `passwordSyncMessage.UPN`, already present).
- Produces: `type SyncStatusRecorder interface { MarkSynced(ctx context.Context, upn string) error; MarkFailed(ctx context.Context, upn string) error }`; `Options.SyncStatusRecorder SyncStatusRecorder` (required, validated non-nil in `New`). Task 7 (app.go) passes `*syncstatus.MemoryStore` here (it structurally satisfies this interface).

- [ ] **Step 1: Write the failing tests**

Add to `internal/worker/worker_test.go` a `fakeSyncStatusRecorder` (unsynchronized, matching `fakeDeadLetterSink`'s convention):

```go
type fakeSyncStatusRecorder struct {
	synced []string
	failed []string
	err    error
}

func (f *fakeSyncStatusRecorder) MarkSynced(_ context.Context, upn string) error {
	f.synced = append(f.synced, upn)
	return f.err
}

func (f *fakeSyncStatusRecorder) MarkFailed(_ context.Context, upn string) error {
	f.failed = append(f.failed, upn)
	return f.err
}
```

Update `newTestWorker` and `newPolicyTestWorker` (the shared helper constructors) to pass `SyncStatusRecorder: &fakeSyncStatusRecorder{}` inside their `Options{...}` literals. Update the direct `New(...)` call in `TestWorkerEmptyReceiveWaitsBeforePollingAgain` the same way.

Rewrite `TestNewValidatesDependencies` so each of the 4 existing nil-dependency sub-cases also includes `SyncStatusRecorder: &fakeSyncStatusRecorder{}` in its `Options{}` (so each sub-case still trips only its own dependency-under-test), and add a 5th sub-case for the new dependency:

```go
func TestNewValidatesDependencies(t *testing.T) {
	baseOptions := func() Options {
		return Options{
			Receiver:           &fakeReceiver{},
			Processor:          &fakeProcessor{},
			PasswordDecrypter:  &fakePasswordDecrypter{},
			DeadLetterSink:     &fakeDeadLetterSink{},
			SyncStatusRecorder: &fakeSyncStatusRecorder{},
			// ...any other existing required fields from the current struct literal, unchanged...
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Options)
		wantErr string
	}{
		{"missing receiver", func(o *Options) { o.Receiver = nil }, "receiver is required"},
		{"missing processor", func(o *Options) { o.Processor = nil }, "processor is required"},
		{"missing password decrypter", func(o *Options) { o.PasswordDecrypter = nil }, "password decrypter is required"},
		{"missing dead letter sink", func(o *Options) { o.DeadLetterSink = nil }, "dead letter sink is required"},
		{"missing sync status recorder", func(o *Options) { o.SyncStatusRecorder = nil }, "worker sync status recorder is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := baseOptions()
			tt.mutate(&opts)

			_, err := New(opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
```

(Reconcile the exact existing error-message strings and any other required `Options` fields against the current file content before finalizing — copy the current test's exact wording for the first 4 cases rather than retyping from memory, and only add the 5th case and the `SyncStatusRecorder` field verbatim as shown.)

Add 3 new standalone tests using direct `New(...)` calls:

```go
func TestWorkerMarksSyncedOnSuccess(t *testing.T) {
	recorder := &fakeSyncStatusRecorder{}
	msg := workerMessage(t, validPasswordSyncMessage(t))

	w, err := New(Options{
		Receiver:           &fakeReceiver{},
		Processor:          &fakeProcessor{},
		PasswordDecrypter:  &fakePasswordDecrypter{},
		DeadLetterSink:     &fakeDeadLetterSink{},
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	w.processMessage(context.Background(), msg)

	if len(recorder.synced) != 1 || recorder.synced[0] != validPasswordSyncMessage(t).UPN {
		t.Errorf("recorder.synced = %v, want [%q]", recorder.synced, validPasswordSyncMessage(t).UPN)
	}
	if len(recorder.failed) != 0 {
		t.Errorf("recorder.failed = %v, want empty", recorder.failed)
	}
}

func TestWorkerMarksFailedOnPermanentProcessorError(t *testing.T) {
	recorder := &fakeSyncStatusRecorder{}
	dlq := &fakeDeadLetterSink{}
	msg := workerMessage(t, validPasswordSyncMessage(t))

	w, err := New(Options{
		Receiver:           &fakeReceiver{},
		Processor:          &fakeProcessor{err: &PermanentError{Err: errors.New("boom")}},
		PasswordDecrypter:  &fakePasswordDecrypter{},
		DeadLetterSink:     dlq,
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	w.processMessage(context.Background(), msg)

	if len(recorder.failed) != 1 || recorder.failed[0] != validPasswordSyncMessage(t).UPN {
		t.Errorf("recorder.failed = %v, want [%q]", recorder.failed, validPasswordSyncMessage(t).UPN)
	}
	if len(recorder.synced) != 0 {
		t.Errorf("recorder.synced = %v, want empty", recorder.synced)
	}
	if len(dlq.entries) != 1 {
		t.Errorf("dlq.entries = %d, want 1", len(dlq.entries))
	}
}

func TestWorkerInvalidMessageDoesNotRecordSyncStatus(t *testing.T) {
	recorder := &fakeSyncStatusRecorder{}
	dlq := &fakeDeadLetterSink{}
	malformed := workerMessage(t, []byte(`{"cn":"jdoe"}`)) // missing upn, malformed for decodePasswordSyncMessage's requirements

	w, err := New(Options{
		Receiver:           &fakeReceiver{},
		Processor:          &fakeProcessor{},
		PasswordDecrypter:  &fakePasswordDecrypter{},
		DeadLetterSink:     dlq,
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	w.processMessage(context.Background(), malformed)

	if len(recorder.synced) != 0 || len(recorder.failed) != 0 {
		t.Errorf("recorder calls = synced:%v failed:%v, want both empty", recorder.synced, recorder.failed)
	}
	if len(dlq.entries) != 1 {
		t.Errorf("dlq.entries = %d, want 1", len(dlq.entries))
	}
}
```

(Reconcile `workerMessage`/`validPasswordSyncMessage`/`fakeProcessor`/`fakeDeadLetterSink` field/parameter names against the actual current file content before pasting — use the exact helper signatures already present in `worker_test.go`. The "malformed" message body must match whatever `decodePasswordSyncMessage` actually requires to fail in the current implementation — check that function's exact validation before finalizing this fixture.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/worker/... -v`
Expected: FAIL — build error, `SyncStatusRecorder`/`Options.SyncStatusRecorder`/`fakeSyncStatusRecorder` undefined.

- [ ] **Step 3: Update worker.go**

Modify `internal/worker/worker.go`. Add the interface definition (near other interface definitions like `Processor`, `DeadLetterSink`):

```go
// SyncStatusRecorder records the outcome of processing a password sync
// message, so that migration.Service can dedupe future login_bootstrap
// events for the same UPN. *syncstatus.MemoryStore implements this.
type SyncStatusRecorder interface {
	MarkSynced(ctx context.Context, upn string) error
	MarkFailed(ctx context.Context, upn string) error
}
```

Add `SyncStatusRecorder SyncStatusRecorder` to the `Options` struct. In `New`, add a nil-check immediately after the existing `PasswordDecrypter` nil-check:

```go
	if opts.PasswordDecrypter == nil {
		return nil, errors.New("worker password decrypter is required")
	}
	if opts.SyncStatusRecorder == nil {
		return nil, errors.New("worker sync status recorder is required")
	}
```

Add `syncStatusRecorder SyncStatusRecorder` field to the `Worker` struct, and set it in the constructor's returned struct literal:

```go
	return &Worker{
		// ...existing fields unchanged...
		syncStatusRecorder: opts.SyncStatusRecorder,
	}, nil
```

In `processMessage`, on the success branch (where `result.err == nil`, right after `settleCtx` is created and before `CompleteMessage` is called), add:

```go
	_ = w.syncStatusRecorder.MarkSynced(settleCtx, passwordSyncMessage.UPN)
```

On the combined transient-exhausted/permanent-failure branch (right before `recordPasswordSyncFailure` is called, using the same `settleCtx`), add:

```go
	_ = w.syncStatusRecorder.MarkFailed(settleCtx, passwordSyncMessage.UPN)
```

Do not add any call on the invalid-message-schema path or the retry-canceled/abandon path — those don't have a resolved `UPN` to record against, or don't represent a final outcome.

- [ ] **Step 4: Run all worker tests to verify they pass**

Run: `go test ./internal/worker/... -v`
Expected: PASS (all tests, old and new)

- [ ] **Step 5: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat(worker): record sync status after processing password sync messages"
```

---

### Task 7: Wire syncstatus into app assembly

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `syncstatus.NewMemoryStore()` (Task 3), `migration.ServiceOptions.SyncStatusStore` (Task 4), `worker.Options.SyncStatusRecorder` (Task 6).
- Produces: `newWithQueue`'s signature gains a 4th positional parameter `syncStatusStore migration.SyncStatusStore` (before the variadic `closers ...appCloser`). No change to `NewWithQueue`'s or `newWithWorkerDependencies`'s public signatures.

- [ ] **Step 1: Update app.go**

Modify `internal/app/app.go`. Add the import:

```go
import (
	// ...existing imports...
	"github.com/nycu/password-hook-service/internal/syncstatus"
)
```

Change `newWithQueue`'s signature to accept the new parameter (insert before the variadic `closers`):

```go
func newWithQueue(cfg Config, queue migration.Queue, passwordEncrypter migration.PasswordEncrypter, syncStatusStore migration.SyncStatusStore, closers ...appCloser) (*App, error) {
```

Inside `newWithQueue`, change the `migration.NewService(...)` call to pass the new options:

```go
	migrationService := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordEncrypter, migration.ServiceOptions{SyncStatusStore: syncStatusStore})
```

In `NewWithQueue` (the public constructor that calls `newWithQueue`), construct a store and pass it:

```go
func NewWithQueue(cfg Config, queue migration.Queue, passwordEncrypter migration.PasswordEncrypter, closers ...appCloser) (*App, error) {
	syncStatusStore := syncstatus.NewMemoryStore()
	return newWithQueue(cfg, queue, passwordEncrypter, syncStatusStore, closers...)
}
```

(Match this to the exact current body of `NewWithQueue` — only the store construction and the extra argument are new; everything else in the function stays as-is.)

In `newWithWorkerDependencies`, construct its own store instance and thread it into both the `newWithQueue` call and the `worker.New` call, so hook and worker share the same in-memory state for that assembly path:

```go
func newWithWorkerDependencies(/* existing parameters unchanged */) (*App, error) {
	syncStatusStore := syncstatus.NewMemoryStore()

	application, err := newWithQueue(cfg, queue, passwordEncrypter, syncStatusStore /* , existing closers args unchanged */)
	if err != nil {
		return nil, err
	}

	w, err := worker.New(worker.Options{
		// ...existing fields unchanged...
		SyncStatusRecorder: syncStatusStore,
	})
	// ...existing error handling and remaining body unchanged...
}
```

(Reconcile this against the exact current body of `newWithWorkerDependencies` — the only changes are: declare `syncStatusStore`, pass it as `newWithQueue`'s 4th arg, and add `SyncStatusRecorder: syncStatusStore` to the `worker.Options{}` literal. All other existing logic, error handling, and parameters remain unchanged.)

- [ ] **Step 2: Update app_test.go**

In `internal/app/app_test.go`, update the 2 direct `newWithQueue(...)` call sites (originally at lines 149 and 296) to pass `nil` as the new 4th positional argument (before the trailing closer/closers args), since neither test exercises sync-status behavior:

```go
// before
application, err := newWithQueue(cfg, queue, passwordEncrypter, closer)
// after
application, err := newWithQueue(cfg, queue, passwordEncrypter, nil, closer)
```

(Match the exact trailing arguments already present at each of the two call sites — only insert `nil` as the new 4th positional argument, keep everything else unchanged.)

The 3 `newWithWorkerDependencies(...)` call sites (originally at lines 206, 243, 324) need no changes, since that function's public signature is unchanged.

Add `"eventType":"login_bootstrap"` to the 4 JSON body fixtures that drive the full HMAC-signed HTTP path and expect success (originally at lines 37, 89, 129, 248):

```go
// before
body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu"}`)
// after
body := []byte(`{"cn":"jdoe","password":"secret123","displayName":"Jane Doe","mail":"jdoe@example.edu","eventType":"login_bootstrap"}`)
```

The `passwordSyncWorkerMessage(t)` fixture helper does not need any change — `decodePasswordSyncMessage` never requires `EventType`.

- [ ] **Step 3: Run all app tests to verify they pass**

Run: `go test ./internal/app/... -v`
Expected: PASS (all tests)

- [ ] **Step 4: Run the full test suite to catch any missed call sites**

Run: `go build ./... && go test ./...`
Expected: PASS with no build errors anywhere in the module.

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): wire syncstatus store into hook and worker assembly"
```

---

### Task 8: Documentation updates

**Files:**
- Modify: `README.md`
- Modify: `docs/examples/sign-hook-request.php`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Update the example request body in README.md**

In `README.md`, find the example curl `--data` body under the "Local HMAC Request" section (currently around line 153) and add `"eventType":"login_bootstrap"` to the JSON payload shown, matching the same style as the existing fields (`cn`, `password`, `displayName`, `mail`).

- [ ] **Step 2: Add an "Event Types and Sync Status" section**

In `README.md`, insert a new section after the existing `## Worker Behavior` section and before `## Configuration`:

```markdown
## Event Types and Sync Status

Every hook request must include an `eventType` field with one of three values:

- `login_bootstrap` — sent after a user completes SSO login and the portal bootstraps their on-prem AD account. The service skips re-enqueueing this event if the UPN is already marked `synced`, or has a `sync_pending` record fresher than the internal pending-sync TTL (300s by default). This avoids redundant AD writes on every login.
- `password_change` — sent when a user changes their password. Always enqueued, regardless of prior sync status.
- `password_recovery` — sent when a user recovers or resets their password. Always enqueued, regardless of prior sync status.

Sync status (`unsynced` / `sync_pending` / `synced` / `sync_failed`) is tracked per-UPN by `internal/syncstatus.MemoryStore`, an in-process, non-durable store: it resets on process restart and is not shared across replicas. This is a deliberate Slice 7A scope limit — Slice 10 (infrastructure) introduces durable, shared sync-status storage. See `docs/superpowers/specs/2026-06-24-password-hook-service-design.md` §1.2.1 Amendment for the full event model rationale.
```

- [ ] **Step 3: Update the PHP example**

In `docs/examples/sign-hook-request.php`, add `eventType` to the payload array, with a comment noting valid values:

```php
$payload = [
    'cn' => 'jdoe',
    // eventType must be one of: login_bootstrap, password_change, password_recovery
    'eventType' => 'login_bootstrap',
    'password' => 'S3cr3tPassw0rd!',
    'displayName' => 'Jane Doe',
    'mail' => 'jdoe@example.edu',
];
```

(Match the exact existing array key order and style already used in the file — only insert the new `eventType` line and its comment; do not reorder or rewrite the other keys.)

- [ ] **Step 4: Commit**

```bash
git add README.md docs/examples/sign-hook-request.php
git commit -m "docs: document eventType and sync status behavior"
```

---

### Task 9: Full verification and plan completion

**Files:** none (verification only, plus final bookkeeping).

- [ ] **Step 1: Build**

Run: `go build ./...`
Expected: no output, exit code 0.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no output, exit code 0.

- [ ] **Step 3: Full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Format check**

Run: `gofmt -l .`
Expected: no output (no files need formatting).

- [ ] **Step 5: Leak scan**

This slice doesn't touch password-handling/encryption code paths directly (only adds an `eventType` field and sync-status bookkeeping), so this is a lighter check than Slice 7's: confirm no new code logs raw password material or `eventType`-adjacent PII.

Run: `grep -rn "log\." internal/syncstatus internal/migration/event.go internal/migration/message.go | grep -iv test`
Expected: no matches, or only unrelated log statements that don't reference `Password`, `password`, or raw UPN/CN values beyond what Slice 7 already logs.

- [ ] **Step 6: Mark this plan completed**

```bash
mkdir -p docs/superpowers/plans/completed
git mv docs/superpowers/plans/active/2026-07-04-slice-07a-portal-password-event-sync-status.md docs/superpowers/plans/completed/2026-07-04-slice-07a-portal-password-event-sync-status.md
```

Edit the moved file's header line to change `> **Plan Status:** Active` to `> **Plan Status:** Completed`.

- [ ] **Step 7: Update roadmap and README**

Update `docs/superpowers/plans/roadmap.md` and `docs/superpowers/plans/README.md`: move the Slice 7A entry from "Active"/"In progress" to "Completed", update its path to point at `completed/2026-07-04-slice-07a-portal-password-event-sync-status.md`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: mark Slice 7A plan completed"
```
