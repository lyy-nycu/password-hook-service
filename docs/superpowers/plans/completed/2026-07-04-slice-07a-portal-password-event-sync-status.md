# Slice 7A: Portal Password Event Semantics and Sync Status Implementation Plan

> **Plan Status:** Completed
>
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
- Any new configuration knobs for the pending-sync TTL beyond reusing the existing `PasswordMessageTTL` value (see Task 4/Task 7 — no new env var or config field is introduced).
- Any change to encryption, HMAC signing, or nonce replay protection (Slice 7 already covers these).

**Known limitations (accepted for this slice, revisit only if they cause real incidents):**
- **Dedupe check-then-act race:** `Submit`'s `login_bootstrap` path does `Get` → (maybe) enqueue → `MarkPending` as three separate steps, not one atomic operation. Two concurrent `login_bootstrap` requests for the same UPN arriving within that narrow window can both pass the check and both enqueue. Impact is bounded to a redundant duplicate sync (the exact class of extra work this slice reduces, not eliminates) — never a correctness or security issue — so a fully atomic claim-based redesign of `syncstatus.Store` is deferred rather than built speculatively.
- **`sourceEnqueuedAt` ordering protects the status record, not write ordering to Entra:** the out-of-order guard (Task 3) guarantees a stale, slow-to-complete message can never overwrite a newer status outcome, but it does not stop that same stale message from calling Microsoft Graph *after* a newer message already did — the bookkeeping stays correct even though the actual last Graph write could still be the older value. This is an inherent property of at-least-once queue processing and is unchanged by this slice.
- **Best-effort `MarkPending`/`MarkSynced`/`MarkFailed`:** every sync-status write after a queue operation is intentionally best-effort — the returned error is silently discarded (`_ = ...`), not logged and never surfaced to the caller, so a status-store hiccup can never fail an otherwise-successful hook request or worker completion. The accepted trade-off is that a write failure at the wrong moment (e.g. immediately before a worker crash) can leave a stale status until the next real event or until `PendingTTL` naturally expires it — the same self-healing window already relied on for a worker crashing before reporting any outcome. (A follow-up slice could add an observability counter/log for these discarded errors if they turn out to matter in practice; not done here to keep this slice's scope to the event/sync-status model itself.)

## Current Context

Slice 7 (password data protection: AES-GCM encryption, HMAC request signing, replay protection) is complete and merged (`docs/superpowers/plans/completed/2026-07-03-slice-07-password-data-protection.md`). The design spec (`docs/superpowers/specs/2026-06-24-password-hook-service-design.md`) has already been amended in this working tree (§1.2.1 Amendment section, updated API contract table with `eventType`, updated Message TTL Expiry section, updated §11.3 PHP example) to correct an earlier false assumption that the hook fires "on every successful login" — it actually fires on `login_bootstrap` (post-SSO bootstrap), `password_change`, and `password_recovery` events from the portal. This plan implements the code changes that make that corrected model real.

This plan was promoted from a draft story that proposed the same event/sync-status model; the draft file itself was deleted from the working tree as part of that promotion (`git show 6fe75b2:docs/superpowers/plans/drafts/2026-07-03-portal-password-event-sync-story.md` to view it from history — it no longer exists at that path on disk). Its "Acceptance Criteria For Promotion" section is fully covered by Task 4's twelve new service-level tests (in particular: `TestServiceEnqueuesLoginBootstrapAfterSyncFailed` for resync-after-`sync_failed`, always-resync for `password_recovery` even when already synced, and the two fail-open/best-effort tests added alongside them).

## File Structure

- Create: `internal/migration/event.go` — `EventType` type, event constants, validation.
- Create: `internal/migration/event_test.go` — validation tests.
- Modify: `internal/migration/message.go` — add `EventType` field to `PasswordSyncMessage`.
- Create: `internal/migration/message_test.go` — round-trip, backward-compat decode, and never-serializes-password tests.
- Create: `internal/syncstatus/status.go` — `Status` type, `Record`, `Store` interface, `MemoryStore` implementation.
- Create: `internal/syncstatus/status_test.go` — `MemoryStore` behavior tests.
- Modify: `internal/migration/service.go` — `ServiceOptions`, `SyncStatusStore` interface, dedupe logic in `Submit`.
- Modify: `internal/migration/service_test.go` — update 5 existing `NewService` call sites, add 12 new tests + `fakeSyncStatusStore`.
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
- Produces: `PasswordSyncMessage.EventType EventType` field with JSON tag `"eventType"`. Task 4's `Submit` sets this field when building the message; Task 6's worker never reads `.EventType` itself (it only reads `.UPN` and `.EnqueuedAt` for sync-status recording, same as its existing fields), but the field must round-trip correctly through the queue for forward compatibility, and it must never cause the plaintext `Password` field to become visible in JSON output.

- [ ] **Step 1: Write the failing test**

Create `internal/migration/message_test.go`. Ground truth from the current `internal/migration/message.go`: `Password` is tagged `json:"-"` (never serialized) and no field uses `,omitempty` — the new `EventType` field must follow that same no-omitempty style:

```go
package migration

import (
	"encoding/json"
	"strings"
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

func TestPasswordSyncMessageNeverSerializesPassword(t *testing.T) {
	msg := PasswordSyncMessage{
		CN:        "jdoe",
		UPN:       "jdoe@example.edu",
		EventType: EventPasswordChange,
		Password:  "cleartext-password",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if strings.Contains(string(data), "cleartext-password") || strings.Contains(string(data), `"password"`) {
		t.Fatalf("marshaled message leaks password: %s", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/migration/... -run TestPasswordSyncMessage -v`
Expected: FAIL — `decoded.EventType`/`msg.EventType` undefined (field doesn't exist yet).

- [ ] **Step 3: Write minimal implementation**

Modify `internal/migration/message.go` — add the `EventType` field to the `PasswordSyncMessage` struct, inserted after `UPN` and before `Password`. This is the exact current struct with only the new field added — no other field or tag changes:

```go
type PasswordSyncMessage struct {
	CN                 string    `json:"cn"`
	UPN                string    `json:"upn"`
	EventType          EventType `json:"eventType"`
	Password           string    `json:"-"`
	PasswordCiphertext string    `json:"passwordCiphertext"`
	PasswordNonce      string    `json:"passwordNonce"`
	PasswordKeyID      string    `json:"passwordKeyId"`
	PasswordAlg        string    `json:"passwordAlg"`
	DisplayName        string    `json:"displayName"`
	Mail               string    `json:"mail"`
	EnqueuedAt         time.Time `json:"enqueuedAt"`
}
```

(Only the new `EventType` line is added; all other fields and tags stay exactly as they are in the current file — verify with `git diff` after editing that no other field was altered.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/migration/... -run TestPasswordSyncMessage -v`
Expected: PASS (all 3 tests)

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
- Consumes: nothing (standalone package; `context`, `sync`, `time` stdlib only — zero repo-internal imports, so `migration` and `worker` can both import it without any import-cycle risk).
- Produces: `type Status string` with constants `StatusUnsynced`, `StatusPending`, `StatusSynced`, `StatusFailed`; `type Record struct { Status Status; UpdatedAt time.Time; SourceEnqueuedAt time.Time }`; `type Store interface { Get(ctx context.Context, upn string) (Record, error); MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error; MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error; MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error }`; `type MemoryStore struct{...}` (unexported fields) implementing `Store`; `func NewMemoryStore() *MemoryStore`. `Get` on an unknown UPN returns `(Record{}, nil)` — i.e. the zero-value `Record` (whose `Status` is the zero value `StatusUnsynced`), not an error. Task 4 consumes `Store.Get`/`MarkPending` via a package-local narrower interface, passing `msg.EnqueuedAt` as `sourceEnqueuedAt`. Task 6 consumes `MarkSynced`/`MarkFailed` via a package-local narrower interface, passing `passwordSyncMessage.EnqueuedAt` as `sourceEnqueuedAt`. Task 7 constructs `*MemoryStore` via `NewMemoryStore()` and passes the same instance to both, so `sourceEnqueuedAt` ordering is compared against writes from both call paths.

**Design note — out-of-order completions:** a naive `{Status, UpdatedAt}` model lets an older, slow-to-process event (e.g. a `login_bootstrap` retry) complete *after* a newer event (e.g. a `password_change`) already completed, silently overwriting the correct state with stale data. The fix: every write carries the triggering message's own `EnqueuedAt` timestamp as `sourceEnqueuedAt`, and `MemoryStore` ignores any write whose `sourceEnqueuedAt` is before the currently-stored record's `SourceEnqueuedAt` — the latest accepted event always wins, regardless of completion order.

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
```

Run with `-race` (see Step 4) to make the concurrent-use test meaningful.

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

func (s *MemoryStore) Get(_ context.Context, upn string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[upn], nil
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
func (s *MemoryStore) set(_ context.Context, upn string, status Status, sourceEnqueuedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.records[upn]; found && sourceEnqueuedAt.Before(existing.SourceEnqueuedAt) {
		return nil
	}
	s.records[upn] = Record{Status: status, UpdatedAt: s.now(), SourceEnqueuedAt: sourceEnqueuedAt}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/syncstatus/... -race -v`
Expected: PASS (all 4 tests)

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
- Consumes: `EventType`/`ValidEventType`/`ErrInvalidEventType` from Task 1; `syncstatus.Record`, `syncstatus.Status` constants from Task 3 (imports `github.com/nycu/password-hook-service/internal/syncstatus`).
- Produces: `type ServiceOptions struct { SyncStatusStore SyncStatusStore; PendingTTL time.Duration }`; `type SyncStatusStore interface { Get(ctx context.Context, upn string) (syncstatus.Record, error); MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error }`; `NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter, opts ...ServiceOptions) *Service` (variadic — existing 3-argument call sites keep compiling unchanged); `Request.EventType EventType` field. Task 5 (hook.go) sets `migration.Request{EventType: ...}` but does **not** need to touch any `migration.NewService(...)` call site, since the new parameter is variadic. Task 7 (app.go) constructs `migration.ServiceOptions{SyncStatusStore: syncStatusStore, PendingTTL: cfg.PasswordMessageTTL}` where `syncStatusStore` is `*syncstatus.MemoryStore` (structurally satisfies `SyncStatusStore`) — wiring `cfg.PasswordMessageTTL` explicitly (rather than leaving `PendingTTL` zero and falling back to `defaultPendingTTL`) keeps the pending-sync freshness window from drifting out of sync with the queue message's own TTL.

Ground truth from the current `internal/migration/service.go` (re-verified): `NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter) *Service` takes exactly 3 args today. `Submit`'s current flow: `defer passwordcrypto.ZeroBytes(req.Password)` → `ClassifyCN` → external email: set `decision.Skipped`/`decision.Reason = "cn_is_external_email"`, return `decision, nil` → unknown identity: return `decision, ErrUnknownIdentity` → `BuildUPN` (sets `decision.UPN = upn`) → nil-checks for `s.queue`/`s.encrypter` → build `PasswordSyncMessage` literal (`CN`, `UPN`, `DisplayName`, `Mail`, `EnqueuedAt: s.now().UTC()`) → `Encrypt` → set ciphertext fields → `EnqueuePasswordSync` → `decision.Enqueued = true`. The 5 existing tests in `service_test.go` construct their fakes as `&captureQueue{}` / `&captureEncrypter{}` / `failingEncrypter{...}` / `failingQueue{...}` (not `fakeQueue`/`fakeEncrypter` — use the real names).

- [ ] **Step 1: Write the failing tests**

Add to `internal/migration/service_test.go` — first, a `fakeSyncStatusStore` test double (place near the existing `captureQueue`/`captureEncrypter` fakes; unsynchronized since each test uses its own private instance):

```go
type fakeSyncStatusStore struct {
	records        map[string]syncstatus.Record
	markPending    []markPendingCall
	getErr         error
	markPendingErr error
}

type markPendingCall struct {
	upn              string
	sourceEnqueuedAt time.Time
}

func newFakeSyncStatusStore() *fakeSyncStatusStore {
	return &fakeSyncStatusStore{records: make(map[string]syncstatus.Record)}
}

func (f *fakeSyncStatusStore) Get(_ context.Context, upn string) (syncstatus.Record, error) {
	if f.getErr != nil {
		return syncstatus.Record{}, f.getErr
	}
	return f.records[upn], nil
}

func (f *fakeSyncStatusStore) MarkPending(_ context.Context, upn string, sourceEnqueuedAt time.Time) error {
	f.markPending = append(f.markPending, markPendingCall{upn: upn, sourceEnqueuedAt: sourceEnqueuedAt})
	if f.markPendingErr != nil {
		return f.markPendingErr
	}
	f.records[upn] = syncstatus.Record{Status: syncstatus.StatusPending, UpdatedAt: time.Now(), SourceEnqueuedAt: sourceEnqueuedAt}
	return nil
}

func (f *fakeSyncStatusStore) setRecord(upn string, status syncstatus.Status, updatedAt time.Time) {
	f.records[upn] = syncstatus.Record{Status: status, UpdatedAt: updatedAt}
}
```

Then add these 12 new test functions to `internal/migration/service_test.go` (all use `t.Parallel()` matching the file's existing style, and `"nycu.edu.tw"` as the primary domain so the resulting UPN matches the rest of the file's fixtures). The last two cover the fail-open (`Get` error) and best-effort (`MarkPending` error) guarantees called out in the plan's "Known limitations" section — a sync-status store hiccup must never fail an otherwise-successful `Submit`:

```go
func TestServiceRejectsInvalidEventType(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

	_, err := service.Submit(context.Background(), Request{
		CN:        "311551001",
		EventType: EventType("bogus"),
		Password:  []byte("secret"),
	})

	if !errors.Is(err, ErrInvalidEventType) {
		t.Fatalf("Submit() error = %v, want ErrInvalidEventType", err)
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceSkipsLoginBootstrapWhenAlreadySynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

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
	if !decision.Skipped {
		t.Error("decision.Skipped = false, want true (already-synced login_bootstrap should skip)")
	}
	if decision.Reason != "already_synced" {
		t.Errorf("decision.Reason = %q, want %q", decision.Reason, "already_synced")
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceSkipsLoginBootstrapWhenPendingFresh(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusPending, time.Now().Add(-1*time.Minute))

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
	if !decision.Skipped {
		t.Error("decision.Skipped = false, want true (fresh pending sync should skip)")
	}
	if decision.Reason != "sync_pending" {
		t.Errorf("decision.Reason = %q, want %q", decision.Reason, "sync_pending")
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceEnqueuesLoginBootstrapWhenPendingStale(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusPending, time.Now().Add(-10*time.Minute))

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
		t.Error("decision.Enqueued = false, want true (stale pending sync should re-enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceEnqueuesLoginBootstrapAfterSyncFailed(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusFailed, time.Now())

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
		t.Error("decision.Enqueued = false, want true (a previously failed sync must re-enqueue on next login)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceAlwaysEnqueuesPasswordChangeEvenWhenSynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    []byte("newsecret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_change always resyncs)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceAlwaysEnqueuesPasswordRecoveryEvenWhenSynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordRecovery,
		Password:    []byte("recoveredsecret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_recovery always resyncs)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceMarksPendingWithSourceEnqueuedAtAfterSuccessfulEnqueue(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})
	fixedNow := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if len(store.markPending) != 1 {
		t.Fatalf("store.markPending = %d calls, want 1", len(store.markPending))
	}
	call := store.markPending[0]
	if call.upn != "311551001@nycu.edu.tw" {
		t.Errorf("markPending upn = %q, want %q", call.upn, "311551001@nycu.edu.tw")
	}
	if !call.sourceEnqueuedAt.Equal(fixedNow) {
		t.Errorf("markPending sourceEnqueuedAt = %v, want %v (must equal msg.EnqueuedAt)", call.sourceEnqueuedAt, fixedNow)
	}
}

func TestServiceNilSyncStatusStoreBehavesLikeNoDedupe(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

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
		t.Error("decision.Enqueued = false, want true (nil store must not block enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceDecisionUPNPopulatedOnSkip(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

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
	if !decision.Skipped {
		t.Fatal("decision.Skipped = false, want true")
	}
	if decision.UPN != upn {
		t.Errorf("decision.UPN = %q, want %q (UPN must be populated even on a skip)", decision.UPN, upn)
	}
}

func TestServiceSkipLoginBootstrapFailsOpenOnStoreGetError(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	store.getErr = errors.New("store unavailable")
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

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
		t.Error("decision.Enqueued = false, want true (a sync-status Get failure must fail open and still enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceEnqueueSucceedsWhenMarkPendingFails(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	store.markPendingErr = errors.New("store unavailable")
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil (a MarkPending failure must never fail an otherwise-successful enqueue)", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}
```

Add `"github.com/nycu/password-hook-service/internal/syncstatus"` to the test file's import block. `errors` is already imported by the existing file (used by the pre-existing `TestServiceRejectsInvalidEventType`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/migration/... -v`
Expected: FAIL — build error. `ServiceOptions`/`SyncStatusStore`/`Request.EventType` don't exist yet.

- [ ] **Step 3: Rewrite service.go**

Modify `internal/migration/service.go`. Add the import:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/syncstatus"
)
```

Add after the `PasswordEncrypter` interface declaration:

```go
// defaultPendingTTL is the fallback used only when ServiceOptions.PendingTTL
// is unset (<= 0), e.g. by callers that don't wire config through. Task 7
// wires the real, single-source-of-truth value explicitly from
// config.Config.PasswordMessageTTL so pending-sync freshness always tracks
// the actual queue message TTL, even if an operator changes it — this
// constant only exists to avoid a zero-TTL footgun for any future caller
// that constructs a Service without going through internal/app.
const defaultPendingTTL = 300 * time.Second

// SyncStatusStore is the narrow view of syncstatus.Store that Service
// needs. *syncstatus.MemoryStore satisfies this interface.
type SyncStatusStore interface {
	Get(ctx context.Context, upn string) (syncstatus.Record, error)
	MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
}

// ServiceOptions configures optional Service behavior.
type ServiceOptions struct {
	// SyncStatusStore, if non-nil, enables login_bootstrap dedupe. If nil,
	// every login_bootstrap event is enqueued unconditionally (dedupe
	// disabled) -- this keeps existing callers backward compatible.
	SyncStatusStore SyncStatusStore
	// PendingTTL bounds how long a sync_pending record suppresses a repeat
	// login_bootstrap enqueue. Defaults to defaultPendingTTL when <= 0.
	// Task 7 always passes cfg.PasswordMessageTTL explicitly here so this
	// value never silently drifts from the queue message's own TTL.
	PendingTTL time.Duration
}
```

Add `EventType EventType` to the `Request` struct, right after `CN`:

```go
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
```

Add `syncStatusStore SyncStatusStore` and `pendingTTL time.Duration` fields to the `Service` struct:

```go
type Service struct {
	primaryDomain   string
	queue           Queue
	encrypter       PasswordEncrypter
	now             func() time.Time
	syncStatusStore SyncStatusStore
	pendingTTL      time.Duration
}
```

Change `NewService` to a variadic-options signature, so all existing 3-argument call sites (in `service_test.go`, `hook_test.go`, and `app.go`) keep compiling unchanged:

```go
// NewService constructs a Service. opts is variadic so existing call sites
// (which pass no options) keep compiling unchanged; passing more than one
// ServiceOptions is invalid usage and only the first is honored.
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
```

In `Submit`, add event-type validation as the very first check, right after `defer passwordcrypto.ZeroBytes(req.Password)`:

```go
func (s *Service) Submit(ctx context.Context, req Request) (Decision, error) {
	defer passwordcrypto.ZeroBytes(req.Password)

	// Defense-in-depth only: internal/handler/hook.go already rejects an
	// invalid/missing eventType before calling Submit, so this path is
	// normally unreachable via the HTTP hook, but must exist for any other
	// future caller of this package.
	if !ValidEventType(req.EventType) {
		return Decision{}, ErrInvalidEventType
	}

	identityType := ClassifyCN(req.CN)
	// ...existing external-email / unknown-identity / BuildUPN logic unchanged...
```

After the existing `decision.UPN = upn` line and before the existing `s.queue`/`s.encrypter` nil checks, add the dedupe check. This mirrors the existing external-email skip pattern (`decision.Skipped` + `decision.Reason`, return `decision, nil`) rather than a bare `Decision{Enqueued: false}` literal, so `decision.UPN` (and `decision.IdentityType`) stay populated on a skip:

```go
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
	// ...existing encrypter nil check unchanged...
```

Add `EventType: req.EventType` to the `PasswordSyncMessage{...}` literal:

```go
	msg := PasswordSyncMessage{
		CN:          strings.TrimSpace(req.CN),
		UPN:         upn,
		EventType:   req.EventType,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Mail:        strings.TrimSpace(req.Mail),
		EnqueuedAt:  s.now().UTC(),
	}
```

After `EnqueuePasswordSync` succeeds and before the final `decision.Enqueued = true; return decision, nil`, add the best-effort pending mark, reusing `msg.EnqueuedAt` (already computed via `s.now().UTC()`) as `sourceEnqueuedAt` rather than calling `s.now()` again:

```go
	if err := s.queue.EnqueuePasswordSync(ctx, msg); err != nil {
		return decision, err
	}

	if s.syncStatusStore != nil {
		// Best-effort: a status-tracking write failure must never fail an
		// otherwise-successful enqueue.
		_ = s.syncStatusStore.MarkPending(ctx, upn, msg.EnqueuedAt)
	}

	decision.Enqueued = true
	return decision, nil
}
```

Add the new helper method at the end of the file:

```go
// skipLoginBootstrap reports whether a login_bootstrap event for upn should
// be skipped because the account is already synced or has a fresh pending
// sync in flight. A store error is treated the same as "no record" (fail
// open): a sync-status outage must never block logins from bootstrapping.
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
```

- [ ] **Step 4: Update the 5 pre-existing `Request` literals**

`NewService`'s 3-argument call sites in `service_test.go` (lines 24, 63, 88, 114, 135) need **no changes** — the new 4th parameter is variadic. But each of the 5 corresponding `Request{...}` literals now needs a valid `EventType`, since `Submit` rejects an empty one before ever reaching `ClassifyCN`. Add `EventType: EventPasswordChange` to each (chosen so these pre-existing tests keep exercising unconditional-enqueue behavior, independent of the new dedupe logic):

```go
	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    []byte("cleartext-password"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})
```

Apply the same `EventType: EventPasswordChange` addition to the `Request` literals in `TestServiceZerosPasswordAfterSuccessfulEnqueue`, `TestServiceZerosPasswordWhenSkippingExternalEmail`, `TestServiceZerosPasswordWhenEncryptFails`, and `TestServiceZerosPasswordWhenQueueFails`.

- [ ] **Step 5: Run all migration tests to verify they pass**

Run: `go test ./internal/migration/... -race -v`
Expected: PASS (all tests, old and new).

- [ ] **Step 6: Commit**

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
- Consumes: `migration.EventType`, `migration.ValidEventType`, `migration.ErrInvalidEventType`, `migration.Request.EventType` (all from Task 1/Task 4). `migration.NewService` is now variadic (Task 4), so none of the 9 existing `migration.NewService(primaryDomain, queue, encrypter)` call sites in `hook_test.go` need any changes.
- Produces: `passwordHookRequest.EventType migration.EventType` (JSON tag `eventType`). No new exported functions — this task only changes request validation and the `migration.Request{}` literal inside the existing `ServeHTTP` handler.

Ground truth from the current `internal/handler/hook.go` (re-verified): `validate()` is a method with signature `func (r passwordHookRequest) validate() string` — it returns a **plain string** (empty means valid), not a struct; each case in its `switch` returns a literal string like `"Field 'cn' is required"`. `ServeHTTP`'s error routing is `if errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrExternalIdentity) { ...400... }` followed by a 500 fallback. There is no `newSignedHookRequest`/`newTestHookHandler` helper anywhere in `hook_test.go` — every existing test builds its own `service := migration.NewService(...)`, `hook := NewHook(service, "https://nycu.edu.tw/problems")`, and `req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))` inline; the two new tests below follow that same pattern.

- [ ] **Step 1: Write the failing tests**

Add to `internal/handler/hook_test.go`, following the existing inline construction style (e.g. `TestHookRejectsUnknownCNAsBadRequest`):

```go
func TestHookRejectsMissingEventType(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "eventType") {
		t.Errorf("body = %q, want mention of eventType", rec.Body.String())
	}
}

func TestHookRejectsInvalidEventType(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	hook := NewHook(service, "https://nycu.edu.tw/problems")

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"password_reset"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "eventType") {
		t.Errorf("body = %q, want mention of eventType", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/handler/... -run TestHookRejects -v`
Expected: FAIL — `TestHookRejectsMissingEventType` gets 202 (empty `eventType` is currently accepted, since `passwordHookRequest` has no `EventType` field yet and `validate()` never checks one), and `TestHookRejectsInvalidEventType` also gets 202 for the same reason.

- [ ] **Step 3: Update hook.go**

Modify `internal/handler/hook.go`. Add `EventType` to the `passwordHookRequest` struct, right after `CN`:

```go
type passwordHookRequest struct {
	CN          string              `json:"cn"`
	EventType   migration.EventType `json:"eventType"`
	Password    passwordBytes       `json:"password"`
	DisplayName string              `json:"displayName"`
	Mail        string              `json:"mail"`
}
```

In the `validate()` method, append two new cases to the switch, after the existing `mail` check and before `default`:

```go
func (r passwordHookRequest) validate() string {
	switch {
	case strings.TrimSpace(r.CN) == "":
		return "Field 'cn' is required"
	case len(r.Password) == 0:
		return "Field 'password' is required"
	case strings.TrimSpace(r.DisplayName) == "":
		return "Field 'displayName' is required"
	case strings.TrimSpace(r.Mail) == "":
		return "Field 'mail' is required"
	case strings.TrimSpace(string(r.EventType)) == "":
		return "Field 'eventType' is required"
	case !migration.ValidEventType(r.EventType):
		return "Field 'eventType' must be one of login_bootstrap, password_change, password_recovery"
	default:
		return ""
	}
}
```

In `ServeHTTP`, add `EventType: body.EventType` to the `migration.Request{...}` literal:

```go
	_, err = h.service.Submit(r.Context(), migration.Request{
		CN:          body.CN,
		EventType:   body.EventType,
		Password:    []byte(body.Password),
		DisplayName: body.DisplayName,
		Mail:        body.Mail,
	})
```

`migration.ErrInvalidEventType` does not need to be added to the `errors.Is` chain: an invalid/missing `eventType` is now rejected by `validate()` before `Submit` is ever called, so `Submit`'s own `ErrInvalidEventType` return path (added in Task 4 as defense-in-depth) is unreachable from this handler. Leave the existing `errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrExternalIdentity)` chain unchanged.

- [ ] **Step 4: Add eventType to the 5 JSON body fixtures that must keep succeeding**

In `internal/handler/hook_test.go`, add `,"eventType":"login_bootstrap"` to the JSON body strings in these 5 tests (originally at lines 26, 51, 266, 287, 305) so they continue to reach and pass the full validation chain:

- `TestHookEnqueuesInternalStudentID` (line 26):
  ```go
  body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
  ```
- `TestHookZerosDecodedPasswordAfterSubmit` (line 51):
  ```go
  body := []byte(`{"cn":"311551001","password":"cleartext-password","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
  ```
- `TestHookSkipsExternalEmailIdentity` (line 266):
  ```go
  body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com","eventType":"login_bootstrap"}`)
  ```
- `TestHookRejectsUnknownCNAsBadRequest` (line 287):
  ```go
  body := []byte(`{"cn":"bad cn!","password":"secret","displayName":"Bad","mail":"bad@nycu.edu.tw","eventType":"login_bootstrap"}`)
  ```
- `TestHookQueueFailureReturnsInternalError` (line 305):
  ```go
  body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
  ```

Do not modify any other JSON body in the file (e.g. lines 73, 92, 120, 135, 189, 207, 322) — those tests either fail at an earlier validation step (missing `cn`/`password`) or bypass `ServeHTTP`/`validate()` entirely (direct `json.Unmarshal` into `passwordHookRequest`, or `decodeJSONStringBytes` calls), so `eventType` is irrelevant to their expected outcome.

- [ ] **Step 5: Run all handler tests to verify they pass**

Run: `go test ./internal/handler/... -race -v`
Expected: PASS (all tests, old and new).

- [ ] **Step 6: Commit**

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
- Consumes: `syncstatus.Record`/`syncstatus.Status` are not referenced directly by this package — only `passwordSyncMessage.UPN` and `passwordSyncMessage.EnqueuedAt` (both already present on `migration.PasswordSyncMessage`).
- Produces: `type SyncStatusRecorder interface { MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error; MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error }`; `Options.SyncStatusRecorder SyncStatusRecorder` (required, validated non-nil in `New`). Task 7 (app.go) passes the same `*syncstatus.MemoryStore` instance used by `migration.ServiceOptions.SyncStatusStore` here — it structurally satisfies this interface too, since its `MarkSynced`/`MarkFailed` methods already take a `sourceEnqueuedAt time.Time` parameter (Task 3).

Ground truth from the current `internal/worker/worker.go` (re-verified): `New(receiver Receiver, processor Processor, options Options) (*Worker, error)` — `receiver`/`processor` are **positional parameters**, not `Options` struct fields. `New`'s nil-checks are sequential `if` statements in this exact order: `receiver == nil` → `"worker receiver is required"`, `processor == nil` → `"worker processor is required"`, `options.DeadLetterSink == nil` → `"worker dead-letter sink is required"`, `options.PasswordDecrypter == nil` → `"worker password decrypter is required"`. `TestNewValidatesDependencies` (currently at line ~608) is **not table-driven** — it's 4 sequential `if _, err := New(...); err == nil || err.Error() != "..." { t.Fatalf(...) }` statements. Test helpers `newTestWorker(t, receiver, processor, decrypter, deadLetters)` (5 params) and `newPolicyTestWorker(t, receiver, processor, decrypter, deadLetters, sleeper)` (6 params) are used by 18 existing test call sites combined — none of them need to change if the helpers default `SyncStatusRecorder` internally (see Step 3). `workerMessage(t, msg migration.PasswordSyncMessage) *Message` takes a `PasswordSyncMessage` value, not `[]byte`. `validPasswordSyncMessage() migration.PasswordSyncMessage` takes **no `t` parameter** and returns `CN: "u1234567"`, `UPN: "u1234567@example.edu"`, `EnqueuedAt: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)`. Every existing test drives the worker via `worker.Run(ctx)` with a `*fakeReceiver` whose `onComplete`/`onAbandon` callback cancels the context (via `fakeDeadLetterSink.onRecord` for DLQ-writing paths) — none of the existing tests call the unexported `processMessage` directly, so the new tests below follow that same `Run`-based convention rather than introducing a new pattern.

- [ ] **Step 1: Write the failing tests**

Add to `internal/worker/worker_test.go` a `fakeSyncStatusRecorder` (unsynchronized, matching the file's other fakes; place it near `fakeDeadLetterSink`):

```go
type fakeSyncStatusRecorder struct {
	synced []syncStatusCall
	failed []syncStatusCall
	err    error
}

type syncStatusCall struct {
	upn              string
	sourceEnqueuedAt time.Time
}

func (f *fakeSyncStatusRecorder) MarkSynced(_ context.Context, upn string, sourceEnqueuedAt time.Time) error {
	f.synced = append(f.synced, syncStatusCall{upn: upn, sourceEnqueuedAt: sourceEnqueuedAt})
	return f.err
}

func (f *fakeSyncStatusRecorder) MarkFailed(_ context.Context, upn string, sourceEnqueuedAt time.Time) error {
	f.failed = append(f.failed, syncStatusCall{upn: upn, sourceEnqueuedAt: sourceEnqueuedAt})
	return f.err
}
```

Add 3 new standalone tests, following the file's existing `Run`-based convention:

```go
func TestWorkerMarksSyncedOnSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := validPasswordSyncMessage()
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, want)}}
	receiver.onComplete = cancel
	recorder := &fakeSyncStatusRecorder{}
	worker, err := New(receiver, &fakeProcessor{}, Options{
		MaxMessages:        10,
		DeadLetterSink:     &fakeDeadLetterSink{},
		PasswordDecrypter:  &fakePasswordDecrypter{plaintext: []byte("cleartext-password")},
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(recorder.synced) != 1 || recorder.synced[0].upn != want.UPN {
		t.Fatalf("recorder.synced = %v, want [{upn: %q}]", recorder.synced, want.UPN)
	}
	if !recorder.synced[0].sourceEnqueuedAt.Equal(want.EnqueuedAt) {
		t.Errorf("recorder.synced[0].sourceEnqueuedAt = %v, want %v", recorder.synced[0].sourceEnqueuedAt, want.EnqueuedAt)
	}
	if len(recorder.failed) != 0 {
		t.Errorf("recorder.failed = %v, want empty", recorder.failed)
	}
}

func TestWorkerMarksFailedOnPermanentProcessorError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := validPasswordSyncMessage()
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, want)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{err: &PermanentError{
		Reason: PermanentReasonProcessorError,
		Err:    errors.New("graph 403"),
	}}
	deadLetters := &fakeDeadLetterSink{}
	recorder := &fakeSyncStatusRecorder{}
	worker, err := New(receiver, processor, Options{
		MaxMessages:        10,
		DeadLetterSink:     deadLetters,
		PasswordDecrypter:  &fakePasswordDecrypter{plaintext: []byte("secret")},
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(recorder.failed) != 1 || recorder.failed[0].upn != want.UPN {
		t.Fatalf("recorder.failed = %v, want [{upn: %q}]", recorder.failed, want.UPN)
	}
	if !recorder.failed[0].sourceEnqueuedAt.Equal(want.EnqueuedAt) {
		t.Errorf("recorder.failed[0].sourceEnqueuedAt = %v, want %v", recorder.failed[0].sourceEnqueuedAt, want.EnqueuedAt)
	}
	if len(recorder.synced) != 0 {
		t.Errorf("recorder.synced = %v, want empty", recorder.synced)
	}
	if len(deadLetters.entries) != 1 {
		t.Errorf("dlq entries = %d, want 1", len(deadLetters.entries))
	}
}

func TestWorkerInvalidMessageDoesNotRecordSyncStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Missing passwordKeyId/passwordAlg/enqueuedAt: decodePasswordSyncMessage
	// fails before a UPN is ever resolved into scope, matching the existing
	// TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal fixture.
	body := []byte(`{"cn":"u1234567","upn":"u1234567@example.edu","passwordCiphertext":"ciphertext","passwordNonce":"nonce"}`)
	receiver := &fakeReceiver{messages: []*Message{{Kind: passwordSyncKind, Body: body}}}
	receiver.onComplete = cancel
	deadLetters := &fakeDeadLetterSink{}
	recorder := &fakeSyncStatusRecorder{}
	worker, err := New(receiver, &fakeProcessor{}, Options{
		MaxMessages:        10,
		DeadLetterSink:     deadLetters,
		PasswordDecrypter:  &fakePasswordDecrypter{},
		SyncStatusRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(deadLetters.entries) != 1 {
		t.Fatalf("safe DLQ entries = %d, want 1", len(deadLetters.entries))
	}
	if len(recorder.synced) != 0 || len(recorder.failed) != 0 {
		t.Errorf("recorder calls = synced:%v failed:%v, want both empty", recorder.synced, recorder.failed)
	}
}
```

Update the direct `New(...)` call in `TestWorkerEmptyReceiveWaitsBeforePollingAgain` to add `SyncStatusRecorder: &fakeSyncStatusRecorder{}`:

```go
	worker, err := New(receiver, processor, Options{
		MaxMessages:        1,
		EmptyReceiveDelay:  50 * time.Millisecond,
		DeadLetterSink:     &fakeDeadLetterSink{},
		PasswordDecrypter:  &fakePasswordDecrypter{},
		SyncStatusRecorder: &fakeSyncStatusRecorder{},
	})
```

Add a 5th case to `TestNewValidatesDependencies`, right after the existing 4 (which stay exactly as they are):

```go
func TestNewValidatesDependencies(t *testing.T) {
	processor := &fakeProcessor{}
	receiver := &fakeReceiver{}

	if _, err := New(nil, processor, Options{DeadLetterSink: &fakeDeadLetterSink{}, PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker receiver is required" {
		t.Fatalf("New with nil receiver error = %v", err)
	}
	if _, err := New(receiver, nil, Options{DeadLetterSink: &fakeDeadLetterSink{}, PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker processor is required" {
		t.Fatalf("New with nil processor error = %v", err)
	}
	if _, err := New(receiver, processor, Options{PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker dead-letter sink is required" {
		t.Fatalf("New without DLQ sink error = %v", err)
	}
	if _, err := New(receiver, processor, Options{DeadLetterSink: &fakeDeadLetterSink{}}); err == nil || err.Error() != "worker password decrypter is required" {
		t.Fatalf("New without decrypter error = %v", err)
	}
	if _, err := New(receiver, processor, Options{DeadLetterSink: &fakeDeadLetterSink{}, PasswordDecrypter: &fakePasswordDecrypter{}}); err == nil || err.Error() != "worker sync status recorder is required" {
		t.Fatalf("New without sync status recorder error = %v", err)
	}
}
```

Update `newTestWorker` and `newPolicyTestWorker` to default `SyncStatusRecorder` to a fresh throwaway `&fakeSyncStatusRecorder{}` internally. This keeps every one of the other 18 existing call sites of these two helpers compiling unchanged, since none of them need to inspect sync-status recording:

```go
func newTestWorker(t *testing.T, receiver Receiver, processor Processor, decrypter PasswordDecrypter, deadLetters DeadLetterSink) *Worker {
	t.Helper()

	worker, err := New(receiver, processor, Options{
		MaxMessages:        10,
		DeadLetterSink:     deadLetters,
		PasswordDecrypter:  decrypter,
		SyncStatusRecorder: &fakeSyncStatusRecorder{},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return worker
}

func newPolicyTestWorker(t *testing.T, receiver Receiver, processor Processor, decrypter PasswordDecrypter, deadLetters *fakeDeadLetterSink, sleeper *fakeSleeper) *Worker {
	t.Helper()

	worker, err := New(receiver, processor, Options{
		MaxMessages:        10,
		DeadLetterSink:     deadLetters,
		PasswordDecrypter:  decrypter,
		Sleep:              sleeper.Sleep,
		Now:                func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		SyncStatusRecorder: &fakeSyncStatusRecorder{},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return worker
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/worker/... -v`
Expected: FAIL — build error. `SyncStatusRecorder`/`Options.SyncStatusRecorder`/`fakeSyncStatusRecorder` don't exist yet.

- [ ] **Step 3: Update worker.go**

Modify `internal/worker/worker.go`. Add the interface definition near the other interface definitions (e.g. after `DeadLetterSink`):

```go
// SyncStatusRecorder records the outcome of processing a password sync
// message, so that migration.Service can dedupe future login_bootstrap
// events for the same UPN. *syncstatus.MemoryStore implements this — its
// MarkSynced/MarkFailed methods already take a sourceEnqueuedAt parameter
// (see internal/syncstatus), which this package forwards from
// passwordSyncMessage.EnqueuedAt so out-of-order message completions can
// never overwrite a newer outcome with stale data.
type SyncStatusRecorder interface {
	MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
	MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error
}
```

Add `SyncStatusRecorder SyncStatusRecorder` to the `Options` struct (after `PasswordDecrypter`). In `New`, add a nil-check immediately after the existing `PasswordDecrypter` nil-check:

```go
	if options.PasswordDecrypter == nil {
		return nil, errors.New("worker password decrypter is required")
	}
	if options.SyncStatusRecorder == nil {
		return nil, errors.New("worker sync status recorder is required")
	}
```

Add a `syncStatusRecorder SyncStatusRecorder` field to the `Worker` struct (after `passwordDecrypter`), and set it in `New`'s returned struct literal:

```go
	return &Worker{
		receiver:           receiver,
		processor:          processor,
		passwordDecrypter:  options.PasswordDecrypter,
		syncStatusRecorder: options.SyncStatusRecorder,
		maxMessages:        options.MaxMessages,
		settlementTimeout:  options.SettlementTimeout,
		emptyReceiveDelay:  options.EmptyReceiveDelay,
		retryBackoffs:      append([]time.Duration(nil), options.RetryBackoffs...),
		deadLetterSink:     options.DeadLetterSink,
		now:                options.Now,
		sleep:              options.Sleep,
	}, nil
```

In `processMessage`'s success branch (`result.err == nil`), add the `MarkSynced` call right after `zeroMessageBody(msg)` and before the settlement context is created (so it's still covered by the same code path, but doesn't consume the settlement timeout):

```go
	result := w.processPasswordSync(ctx, passwordSyncMessage)
	if result.err == nil {
		zeroMessageBody(msg)
		_ = w.syncStatusRecorder.MarkSynced(ctx, passwordSyncMessage.UPN, passwordSyncMessage.EnqueuedAt)
		settleCtx, cancel := w.settlementContext()
		defer cancel()
		if settleErr := w.receiver.CompleteMessage(settleCtx, msg); settleErr != nil {
			return fmt.Errorf("complete worker message: %w", settleErr)
		}
		return nil
	}
```

On the combined transient-exhausted/permanent-failure branch (the one that builds the `DeadLetterEntry` with `reason`/`description`), add the `MarkFailed` call right after `zeroMessageBody(msg)`, before the settlement context is created:

```go
	zeroMessageBody(msg)
	_ = w.syncStatusRecorder.MarkFailed(ctx, passwordSyncMessage.UPN, passwordSyncMessage.EnqueuedAt)
	settleCtx, cancel := w.settlementContext()
	defer cancel()
	if settleErr := w.recordPasswordSyncFailure(settleCtx, DeadLetterEntry{
```

Do not add any call on the invalid-message-schema path (top of `processMessage`, where `decodePasswordSyncMessage` fails) or the retry-canceled/abandon path — the former never has a `passwordSyncMessage.UPN` resolved into scope (decoding failed before that variable was populated), and the latter isn't a final/terminal outcome (the message will be retried by the queue).

Both new calls use `ctx` (the original processing context), not `settleCtx` — `settleCtx` is a short-lived timeout scoped only to the final receiver settlement (`CompleteMessage`/`AbandonMessage`/DLQ write) and is created *after* these calls in the source, so reusing `ctx` here avoids restructuring the existing control flow just to move a context's construction earlier.

- [ ] **Step 4: Run all worker tests to verify they pass**

Run: `go test ./internal/worker/... -race -v`
Expected: PASS (all tests, old and new).

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
- Produces: `newWithQueue`'s signature gains a 4th positional parameter `syncStatusStore migration.SyncStatusStore` (inserted before the variadic `closers ...appCloser`). No change to `NewWithQueue`'s or `newWithWorkerDependencies`'s exported/internal call signatures — both already exist and stay exactly as-is (`newWithWorkerDependencies` itself is unexported, only `newWithQueue`'s internal signature changes).

Ground truth from the current `internal/app/app.go` (re-verified): `NewWithQueue(cfg config.Config, queue migration.Queue) (*App, error)` takes exactly **2 parameters** today — it builds its own `passwordCodec` internally via `newPasswordCodec(cfg)` and calls `newWithQueue(cfg, queue, passwordCodec)`. `newWithWorkerDependencies(cfg, queue, receiver, processor, deadLetterSink, passwordCodec, closers...)` calls `newWithQueue(cfg, queue, passwordCodec, closers...)` internally, then separately calls `worker.New(receiver, processor, worker.Options{DeadLetterSink: deadLetterSink, PasswordDecrypter: passwordCodec})`. From `internal/app/app_test.go` (re-verified): exactly 2 direct `newWithQueue(...)` call sites, at lines 149 and 296, both of the form `newWithQueue(cfg, &captureQueue{}, mustPasswordCodec(t, cfg), closer)`; 3 `newWithWorkerDependencies(...)` call sites at lines 206, 243, 324 (these need no changes — that function's own signature is unchanged); JSON bodies needing `eventType` are at lines 37, 89, 129, 248 (`TestAppHookRouteEnqueuesInternalIdentity`, `TestAppHookRouteQueuesCiphertextOnlyMessage`, `TestAppHookRouteSkipsExternalEmailWithoutEnqueue`, `TestNewWithWorkerDependenciesSharesPasswordCodecWithHookAndWorker`).

- [ ] **Step 1: Update app.go**

Modify `internal/app/app.go`. Add the import:

```go
import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/graphprocessor"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/servicebusqueue"
	"github.com/nycu/password-hook-service/internal/syncstatus"
	"github.com/nycu/password-hook-service/internal/worker"
)
```

Change `NewWithQueue` to construct a `*syncstatus.MemoryStore` and pass it through:

```go
func NewWithQueue(cfg config.Config, queue migration.Queue) (*App, error) {
	if err := cfg.ValidateHTTP(); err != nil {
		return nil, err
	}
	if err := validatePasswordEncryptionConfig(cfg); err != nil {
		return nil, err
	}
	if queue == nil {
		return nil, errors.New("migration queue is required")
	}
	passwordCodec, err := newPasswordCodec(cfg)
	if err != nil {
		return nil, err
	}
	return newWithQueue(cfg, queue, passwordCodec, syncstatus.NewMemoryStore())
}
```

Change `newWithWorkerDependencies` to construct its own store instance and thread the same instance into both the `newWithQueue` call and the `worker.New` call, so the hook and the worker share sync-status state for that assembly path:

```go
func newWithWorkerDependencies(
	cfg config.Config,
	queue migration.Queue,
	receiver worker.Receiver,
	processor worker.Processor,
	deadLetterSink worker.DeadLetterSink,
	passwordCodec passwordCodec,
	closers ...appCloser,
) (*App, error) {
	if passwordCodec == nil {
		return nil, errors.Join(errors.New("password codec is required"), closeAppResources(context.Background(), closers))
	}
	syncStatusStore := syncstatus.NewMemoryStore()
	application, err := newWithQueue(cfg, queue, passwordCodec, syncStatusStore, closers...)
	if err != nil {
		return nil, err
	}
	passwordWorker, err := worker.New(receiver, processor, worker.Options{
		DeadLetterSink:     deadLetterSink,
		PasswordDecrypter:  passwordCodec,
		SyncStatusRecorder: syncStatusStore,
	})
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	application.worker = passwordWorker
	return application, nil
}
```

Change `newWithQueue` to accept and use the new parameter:

```go
func newWithQueue(cfg config.Config, queue migration.Queue, passwordEncrypter migration.PasswordEncrypter, syncStatusStore migration.SyncStatusStore, closers ...appCloser) (*App, error) {
	if passwordEncrypter == nil {
		return nil, errors.Join(errors.New("password encrypter is required"), closeAppResources(context.Background(), closers))
	}
	service := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordEncrypter, migration.ServiceOptions{
		SyncStatusStore: syncStatusStore,
		// Reuse the queue message's own TTL as the pending-sync freshness
		// window, so a sync_pending record never outlives (or silently
		// drifts from) the message it corresponds to — see Task 4's
		// defaultPendingTTL comment and the plan's "Known limitations".
		PendingTTL: cfg.PasswordMessageTTL,
	})
	hook := handler.NewHook(service, cfg.ProblemBaseURL)
	hmacMiddleware, err := middleware.NewHMACWithProblemBase(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, cfg.ProblemBaseURL)
	if err != nil {
		return nil, errors.Join(err, closeAppResources(context.Background(), closers))
	}
	rateLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		AllowedCIDRs: cfg.PortalAllowedCIDRs,
		LimitPerIP:   cfg.RateLimitPerIP,
		Window:       cfg.RateLimitWindow,
		ProblemBase:  cfg.ProblemBaseURL,
	})

	hookHandler := hmacMiddleware.Wrap(hook)
	hookHandler = rateLimiter.Wrap(hookHandler)
	hookHandler = middleware.RecoveryWithProblemBase(slog.Default(), cfg.ProblemBaseURL)(hookHandler)
	hookHandler = middleware.AccessLog(slog.Default())(hookHandler)
	hookHandler = requestid.Middleware(hookHandler)

	server := httpserver.New(cfg.HTTPAddr, httpserver.Routes{
		Hook: hookHandler,
	}, buildinfo.Current())

	return &App{server: server, closers: append([]appCloser(nil), closers...)}, nil
}
```

(Note: `migration.ServiceOptions{SyncStatusStore: syncStatusStore, PendingTTL: cfg.PasswordMessageTTL}` is safe even when `syncStatusStore` is a nil interface value — `migration.Service.Submit` only calls into it after checking `s.syncStatusStore != nil`, per Task 4. Passing `cfg.PasswordMessageTTL` explicitly here — rather than leaving `PendingTTL` unset and relying on Task 4's `defaultPendingTTL` fallback — is a deliberate fix: it ties the pending-sync freshness window to the same single config value that governs the queue message's actual TTL, so the two can never drift apart if `PasswordMessageTTL` is ever changed.)

- [ ] **Step 2: Update app_test.go**

In `internal/app/app_test.go`, update the 2 direct `newWithQueue(...)` call sites (at lines 149 and 296) to pass `nil` as the new 4th positional argument, before the trailing closer argument, since neither test exercises sync-status behavior:

```go
// TestNewWithQueueClosesOwnedQueueWhenAppWiringFails (line 149)
application, err := newWithQueue(cfg, &captureQueue{}, mustPasswordCodec(t, cfg), nil, closer)
```

```go
// TestRunClosesQueueWithBoundedContextFromCallerContext (line 296)
application, err := newWithQueue(cfg, &captureQueue{}, mustPasswordCodec(t, cfg), nil, closer)
```

The 3 `newWithWorkerDependencies(...)` call sites (lines 206, 243, 324) need no changes, since that function's own signature is unchanged.

Add `,"eventType":"login_bootstrap"` to the 4 JSON body fixtures that drive the full HMAC-signed HTTP path and expect a `202 Accepted`:

```go
// TestAppHookRouteEnqueuesInternalIdentity (line 37)
body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
```

```go
// TestAppHookRouteQueuesCiphertextOnlyMessage (line 89)
body := []byte(`{"cn":"311551001","password":"cleartext-password","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
```

```go
// TestAppHookRouteSkipsExternalEmailWithoutEnqueue (line 129)
body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com","eventType":"login_bootstrap"}`)
```

```go
// TestNewWithWorkerDependenciesSharesPasswordCodecWithHookAndWorker (line 248)
body := []byte(`{"cn":"311551001","password":"hook-password","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
```

The `passwordSyncWorkerMessage(t)` fixture helper (used by the worker side of `TestNewWithWorkerDependenciesSharesPasswordCodecWithHookAndWorker`) does not need any change — it builds a `migration.PasswordSyncMessage` directly, and `decodePasswordSyncMessage` (Task 6) never requires `EventType`.

- [ ] **Step 3: Run all app tests to verify they pass**

Run: `go test ./internal/app/... -race -v`
Expected: PASS (all tests).

- [ ] **Step 4: Run the full test suite to catch any missed call sites**

Run: `go build ./... && go test ./... -race`
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

In `README.md`, the "Local HMAC Request" section's curl example (currently line 153) reads:

```bash
  --data '{"cn":"311551001","password":"cleartext_password","displayName":"Test User","mail":"test@nycu.edu.tw"}'
```

Add `"eventType":"login_bootstrap"` to the JSON payload, matching the same style as the existing fields:

```bash
  --data '{"cn":"311551001","password":"cleartext_password","displayName":"Test User","mail":"test@nycu.edu.tw","eventType":"login_bootstrap"}'
```

- [ ] **Step 2: Add an "Event Types and Sync Status" section**

In `README.md`, insert a new section after the existing `## Worker Behavior` section (currently ends at line 164, right before `## Configuration` at line 166):

```markdown
## Event Types and Sync Status

Every hook request must include an `eventType` field with one of three values:

- `login_bootstrap` — sent after a user completes SSO login and the portal bootstraps their on-prem AD account. The service skips re-enqueueing this event if the UPN is already marked `synced`, or has a `sync_pending` record fresher than the internal pending-sync TTL (300s by default). This avoids redundant AD writes on every login.
- `password_change` — sent when a user changes their password. Always enqueued, regardless of prior sync status.
- `password_recovery` — sent when a user recovers or resets their password. Always enqueued, regardless of prior sync status.

Sync status (`unsynced` / `sync_pending` / `synced` / `sync_failed`) is tracked per-UPN by `internal/syncstatus.MemoryStore`, an in-process, non-durable store: it resets on process restart and is not shared across replicas. This is a deliberate Slice 7A scope limit — Slice 10 (infrastructure) introduces durable, shared sync-status storage. See `docs/superpowers/specs/2026-06-24-password-hook-service-design.md` §1.2.1 Amendment for the full event model rationale.
```

- [ ] **Step 3: Update the PHP example**

The current `docs/examples/sign-hook-request.php` builds its JSON payload with `json_encode` directly (no intermediate array variable):

```php
$payload = json_encode([
    'cn' => '311551001',
    'password' => 'cleartext_password',
    'displayName' => 'Test User',
    'mail' => 'test@nycu.edu.tw',
]);
```

Add `eventType` as the last key, with a comment above it noting the valid values. Keep every other line (including the surrounding `$timestamp`/`$nonce`/`$secret`/`$signature`/`echo` lines) exactly as-is:

```php
$payload = json_encode([
    'cn' => '311551001',
    'password' => 'cleartext_password',
    'displayName' => 'Test User',
    'mail' => 'test@nycu.edu.tw',
    // eventType must be one of: login_bootstrap, password_change, password_recovery
    'eventType' => 'login_bootstrap',
]);
```

- [ ] **Step 4: Commit**

```bash
git add README.md docs/examples/sign-hook-request.php
git commit -m "docs: document eventType and sync status behavior"
```

---

### Task 9: Full verification and plan completion

**Files:**
- Modify: `docs/superpowers/plans/roadmap.md`
- Modify: `docs/superpowers/plans/README.md`
- Move: `docs/superpowers/plans/active/2026-07-04-slice-07a-portal-password-event-sync-status.md` → `docs/superpowers/plans/completed/2026-07-04-slice-07a-portal-password-event-sync-status.md`

**Interfaces:**
- Consumes: nothing (verification and bookkeeping only).
- Produces: nothing (final task in the plan).

- [ ] **Step 1: Build**

Run: `go build ./...`
Expected: no output, exit code 0.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no output, exit code 0.

- [ ] **Step 3: Full test suite**

Run: `go test ./... -race`
Expected: all packages `ok`, no failures.

- [ ] **Step 4: Format check**

Run: `gofmt -l .`
Expected: no output (no files need formatting).

- [ ] **Step 5: Leak scan**

This slice touches `service.go`, `hook.go`, and `worker.go` in addition to the new `syncstatus`/`event.go`/`message.go` code, so the scan must cover all of them, not just the new files: confirm no new code logs raw password material.

Run: `grep -rn "log\." internal/syncstatus internal/migration/event.go internal/migration/message.go internal/migration/service.go internal/handler/hook.go internal/worker/worker.go internal/app/app.go | grep -iv test`
Expected: no matches, or only unrelated log statements that don't reference `Password`, `password`, or raw password bytes.

- [ ] **Step 6: Mark this plan completed**

Edit this file's header line (before moving it) to change:

```markdown
> **Plan Status:** Active
```

to:

```markdown
> **Plan Status:** Completed
```

Then move it into `completed/`:

```bash
git mv docs/superpowers/plans/active/2026-07-04-slice-07a-portal-password-event-sync-status.md docs/superpowers/plans/completed/2026-07-04-slice-07a-portal-password-event-sync-status.md
```

- [ ] **Step 7: Update roadmap.md**

In `docs/superpowers/plans/roadmap.md`, update the "Active Detailed Plan" section (currently line 35):

```markdown
Current active detailed plan: `active/2026-07-04-slice-07a-portal-password-event-sync-status.md` (Slice 7A Portal Password Event Semantics and Sync Status). Promoted from the draft after refreshing against the final Slice 7 implementation and amending the source design spec; ready for execution.
```

to:

```markdown
No plan is currently active. Slice 7A (Portal Password Event Semantics and Sync Status) is complete; see `completed/2026-07-04-slice-07a-portal-password-event-sync-status.md`. Promote the next slice from `drafts/` once its assumptions are refreshed against the Slice 7A event/sync-status model (see Slice Boundaries below).
```

Update the Completion Tracking table row for Slice 7A (currently line 69):

```markdown
| 7A. Portal Password Event Semantics and Sync Status | Active | `active/2026-07-04-slice-07a-portal-password-event-sync-status.md` | Promoted from draft; source design spec amended (§1.2.1); ready for execution via subagent-driven-development or executing-plans |
```

to:

```markdown
| 7A. Portal Password Event Semantics and Sync Status | Done | `completed/2026-07-04-slice-07a-portal-password-event-sync-status.md` | `eventType` added to hook request and wire message; `login_bootstrap` skipped once synced or pending within TTL; `password_change`/`password_recovery` always enqueue; worker records `synced`/`sync_failed` after Graph outcome using an ordering guard against out-of-order completions; verified with focused package tests, full `go test ./... -race`, `go vet ./...`, and leak-focused `rg` scans |
```

- [ ] **Step 8: Update README.md**

In `docs/superpowers/plans/README.md`, update the "Current Active Plan" section (currently line 26):

```markdown
Current active detailed plan: `active/2026-07-04-slice-07a-portal-password-event-sync-status.md` (Slice 7A Portal Password Event Semantics and Sync Status). See `roadmap.md` for details.
```

to:

```markdown
No plan is currently active. Slice 7A (Portal Password Event Semantics and Sync Status) is complete; see `roadmap.md` for the next slice to promote from `drafts/`.
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "chore: mark Slice 7A plan completed"
```
