# Slice 8 Observability Draft Implementation Plan

> **Status:** Draft. This plan is a future-slice planning artifact only. Do not execute it until Slice 7 is merged, this draft is refreshed against `main`, and the plan is promoted to `docs/superpowers/plans/active/`.
>
> **For agentic workers:** REQUIRED SUB-SKILL WHEN PROMOTED: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add operational logs, metrics, and traceability for the hook, worker, Graph processor, and queue/DLQ depth paths without logging or persisting password material.

**Architecture:** Introduce a small internal observability package with event-name constants, field helpers, and a `Recorder` interface for counters/timers/gauges. Wire no-op defaults through existing app construction so tests can inject capturing recorders without requiring Azure Monitor. Keep Azure Monitor export as an adapter boundary and expose queue depth through a probe interface that infrastructure can later connect to Azure Monitor.

**Tech Stack:** Go `log/slog`, existing `requestid` context values, existing `pkg/logger` masking handler, Azure Service Bus SDK receiver/sender boundaries, unit tests with in-memory fakes.

---

## Draft Constraints

- Do not start this implementation while Slice 7 is active unless the owner explicitly confirms it is independent from Slice 7's password lifecycle changes.
- Refresh all touched files against the latest `main` before promotion because Slice 7 may change worker, logging, or leak-test behavior.
- Do not add OpenTelemetry, Prometheus, or Azure SDK exporter dependencies in this slice unless a later decision explicitly chooses an exporter. This slice should define stable instrumentation points and testable interfaces.
- Do not record cleartext passwords, password ciphertext, password nonce, password key IDs, request bodies, Service Bus message bodies, Graph authorization headers, or Graph request bodies in logs or metrics labels.

## Current Context

- `internal/middleware/accesslog.go` already emits request logs with `traceId`, `method`, `path`, `status`, and `durationMs`.
- `internal/requestid/requestid.go` carries request IDs through HTTP request contexts, but worker messages do not currently carry trace IDs.
- `internal/migration/service.go` returns a `Decision` with `Enqueued`, `Skipped`, and `Reason`, but the hook handler currently discards it.
- `internal/worker/worker.go` implements retry, safe DLQ, completion, and abandon behavior, but it does not emit structured worker outcome events or counters.
- `internal/graphprocessor/processor.go` maps Graph failures to worker permanent/transient behavior, but it does not record Graph operation latency or result classification.
- `internal/servicebusqueue` sends and receives Service Bus messages but does not expose active queue depth or safe DLQ depth probes.
- `pkg/logger` masks `password`, `passwd`, and `secret` fields. Slice 8 must preserve and extend leak-focused tests around any new logging.

## File Structure

- Create `internal/observability/recorder.go`: no-op and in-memory-testable metric recorder interfaces.
- Create `internal/observability/events.go`: event names and helper functions for structured `slog` fields.
- Create `internal/observability/recorder_test.go`: no-op behavior and label-copying tests.
- Modify `internal/migration/message.go`: add non-secret `TraceID string` JSON field to password sync messages.
- Modify `internal/migration/service.go`: propagate trace ID from context into queued messages.
- Modify `internal/migration/service_test.go`: assert trace ID is queued and no password material is added to metadata.
- Modify `internal/handler/hook.go`: preserve `Decision`, emit hook outcome event, and record hook counters.
- Modify `internal/handler/hook_test.go`: assert enqueue, skip, validation, and error instrumentation.
- Modify `internal/worker/worker.go`: add recorder/logger options and emit worker receive, success, retry exhausted, permanent failure, invalid message, abandon, and DLQ write outcomes.
- Modify `internal/worker/worker_test.go`: assert counters/events for worker outcomes without password leaks.
- Modify `internal/graphprocessor/processor.go`: measure Graph processor duration and classify success/permanent/transient outcomes.
- Modify `internal/graphprocessor/processor_test.go`: assert Graph processor metrics and event classification.
- Modify `internal/servicebusqueue/queue.go` and `internal/servicebusqueue/deadletter.go`: add queue depth probe interfaces without changing send/receive behavior.
- Modify `internal/servicebusqueue/queue_test.go`: assert depth probe maps active and safe-DLQ counts.
- Modify `internal/app/app.go`: wire no-op recorder/logger defaults through production and test constructors.
- Modify `internal/app/app_test.go`: assert app wiring keeps existing hook and worker behavior while instrumentation is enabled.
- Modify `README.md`: document observability signals, metric names, labels, and non-secret constraints.

---

### Task 1: Observability Recorder Foundation

**Files:**
- Create: `internal/observability/recorder.go`
- Create: `internal/observability/events.go`
- Create: `internal/observability/recorder_test.go`

- [ ] **Step 1: Write failing tests for no-op and capturing recorder behavior**

Create `internal/observability/recorder_test.go`:

```go
package observability

import (
	"context"
	"testing"
	"time"
)

func TestNoopRecorderAcceptsAllSignals(t *testing.T) {
	recorder := NoopRecorder{}

	recorder.Inc(context.Background(), "hook_requests_total", Labels{"status": "202"})
	recorder.ObserveDuration(context.Background(), "graph_api_latency", 12*time.Millisecond, Labels{"operation": "upsert"})
	recorder.SetGauge(context.Background(), "queue_depth", 7, Labels{"queue": "password-sync"})
}

func TestCaptureRecorderCopiesLabels(t *testing.T) {
	recorder := NewCaptureRecorder()
	labels := Labels{"status": "202"}

	recorder.Inc(context.Background(), "hook_requests_total", labels)
	labels["status"] = "500"

	got := recorder.Counters("hook_requests_total")
	if len(got) != 1 {
		t.Fatalf("counter samples = %d, want 1", len(got))
	}
	if got[0].Labels["status"] != "202" {
		t.Fatalf("stored status = %q, want original 202", got[0].Labels["status"])
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `/usr/local/go/bin/go test ./internal/observability`

Expected: FAIL because `internal/observability` does not exist.

- [ ] **Step 3: Implement minimal recorder interfaces and test helper**

Create `internal/observability/recorder.go`:

```go
package observability

import (
	"context"
	"sync"
	"time"
)

type Labels map[string]string

type Recorder interface {
	Inc(context.Context, string, Labels)
	ObserveDuration(context.Context, string, time.Duration, Labels)
	SetGauge(context.Context, string, int64, Labels)
}

type NoopRecorder struct{}

func (NoopRecorder) Inc(context.Context, string, Labels) {}
func (NoopRecorder) ObserveDuration(context.Context, string, time.Duration, Labels) {}
func (NoopRecorder) SetGauge(context.Context, string, int64, Labels) {}

type Sample struct {
	Name     string
	Labels   Labels
	Duration time.Duration
	Value    int64
}

type CaptureRecorder struct {
	mu        sync.Mutex
	counters  map[string][]Sample
	durations map[string][]Sample
	gauges    map[string][]Sample
}

func NewCaptureRecorder() *CaptureRecorder {
	return &CaptureRecorder{
		counters:  make(map[string][]Sample),
		durations: make(map[string][]Sample),
		gauges:    make(map[string][]Sample),
	}
}

func (r *CaptureRecorder) Inc(_ context.Context, name string, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name] = append(r.counters[name], Sample{Name: name, Labels: copyLabels(labels), Value: 1})
}

func (r *CaptureRecorder) ObserveDuration(_ context.Context, name string, duration time.Duration, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations[name] = append(r.durations[name], Sample{Name: name, Labels: copyLabels(labels), Duration: duration})
}

func (r *CaptureRecorder) SetGauge(_ context.Context, name string, value int64, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[name] = append(r.gauges[name], Sample{Name: name, Labels: copyLabels(labels), Value: value})
}

func (r *CaptureRecorder) Counters(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.counters[name]...)
}

func copyLabels(labels Labels) Labels {
	out := make(Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
```

- [ ] **Step 4: Add event names and safe field helpers**

Create `internal/observability/events.go`:

```go
package observability

import "log/slog"

const (
	ActionHookAccepted       = "hook_password_sync_accepted"
	ActionHookSkipped        = "hook_password_sync_skipped"
	ActionHookRejected       = "hook_password_sync_rejected"
	ActionWorkerCompleted    = "worker_password_sync_completed"
	ActionWorkerFailed       = "worker_password_sync_failed"
	ActionWorkerInvalid      = "worker_message_invalid"
	ActionWorkerAbandoned    = "worker_message_abandoned"
	ActionGraphUpsert        = "graph_password_upsert"
)

func SafeIdentityAttrs(traceID string, cn string, upn string) []slog.Attr {
	attrs := []slog.Attr{}
	if traceID != "" {
		attrs = append(attrs, slog.String("traceId", traceID))
	}
	if cn != "" {
		attrs = append(attrs, slog.String("cn", cn))
	}
	if upn != "" {
		attrs = append(attrs, slog.String("upn", upn))
	}
	return attrs
}
```

- [ ] **Step 5: Run the test and verify it passes**

Run: `/usr/local/go/bin/go test ./internal/observability`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/observability/recorder.go internal/observability/events.go internal/observability/recorder_test.go
git commit -m "feat: add observability recorder abstraction"
```

### Task 2: Hook Outcome Logs And Counters

**Files:**
- Modify: `internal/handler/hook.go`
- Modify: `internal/handler/hook_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing hook instrumentation tests**

Add tests that construct a hook with a capture recorder and logger, then assert:

```go
// accepted internal identity:
// - counter hook_requests_total has labels status=202,outcome=enqueued
// - log contains action=hook_password_sync_accepted, traceId, cn, upn, outcome=enqueued

// external email identity:
// - counter migration_skipped_total has labels reason=cn_is_external_email
// - log contains action=hook_password_sync_skipped and no password

// validation failure:
// - counter hook_requests_total has labels status=400,outcome=validation_error
// - log contains action=hook_password_sync_rejected
```

- [ ] **Step 2: Run hook tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/handler`

Expected: FAIL because `Hook` does not accept recorder/logger options.

- [ ] **Step 3: Add hook options and emit events**

Change `Hook` construction to keep default behavior:

```go
type HookOptions struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
}

func NewHook(service *migration.Service, problemBaseURL string) *Hook {
	return NewHookWithOptions(service, problemBaseURL, HookOptions{})
}
```

Record counters after `Submit` returns, using the `Decision` currently discarded by `ServeHTTP`. Never log `body.Password`.

- [ ] **Step 4: Wire app defaults**

In `internal/app/app.go`, pass `slog.Default()` and `observability.NoopRecorder{}` through `NewHookWithOptions`. Keep `NewWithQueue` tests working without custom setup.

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/handler ./internal/app
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/hook.go internal/handler/hook_test.go internal/app/app.go
git commit -m "feat: record hook observability outcomes"
```

### Task 3: Trace ID Propagation Into Worker Messages

**Files:**
- Modify: `internal/migration/message.go`
- Modify: `internal/migration/service.go`
- Modify: `internal/migration/service_test.go`
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing tests for trace propagation**

Add a migration service test:

```go
ctx := requestid.With(context.Background(), "trace-123")
decision, err := service.Submit(ctx, migration.Request{CN: "311551001", Password: "secret", DisplayName: "Student", Mail: "student@nycu.edu.tw"})
// assert err == nil, decision.Enqueued == true, queued message TraceID == "trace-123"
```

Add a worker test that processes a message with `traceId:"trace-123"` and asserts the worker event log includes `traceId`.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
/usr/local/go/bin/go test ./internal/migration ./internal/worker
```

Expected: FAIL because `PasswordSyncMessage` has no `TraceID`.

- [ ] **Step 3: Add non-secret trace field**

In `internal/migration/message.go`:

```go
TraceID string `json:"traceId,omitempty"`
```

In `Submit`, set `TraceID: requestid.From(ctx)`.

- [ ] **Step 4: Include trace ID in worker command context**

Add `TraceID string` to `worker.PasswordSyncCommand` so downstream processors and logs can correlate Graph work without reading the encrypted message body.

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/migration ./internal/worker ./internal/graphprocessor
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/migration/message.go internal/migration/service.go internal/migration/service_test.go internal/worker/worker.go internal/worker/worker_test.go internal/graphprocessor/processor.go internal/graphprocessor/processor_test.go
git commit -m "feat: propagate trace id through password sync messages"
```

### Task 4: Worker Outcome Metrics And Logs

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing worker observability tests**

Add tests for these cases:

```text
success:
  counter migration_success_total labels outcome=success
  log action=worker_password_sync_completed, traceId, cn, upn, attempts

permanent processor error:
  counter migration_failed_total labels reason=permanent_processor_error
  log action=worker_password_sync_failed, permanent=true, attempts=1

transient retries exhausted:
  counter migration_failed_total labels reason=transient_processor_retries_exhausted
  log action=worker_password_sync_failed, attempts=4

invalid schema:
  counter migration_failed_total labels reason=invalid_message_schema
  log action=worker_message_invalid
```

Every test must assert logs do not contain known test password strings, ciphertext, nonce, or `passwordCiphertext`.

- [ ] **Step 2: Run worker tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/worker`

Expected: FAIL because worker has no recorder/logger options.

- [ ] **Step 3: Add worker observability options**

Extend `worker.Options`:

```go
Logger   *slog.Logger
Recorder observability.Recorder
```

Default `Recorder` to `observability.NoopRecorder{}` when nil. Keep nil logger as no logs.

- [ ] **Step 4: Emit outcome events at settlement boundaries**

Record metrics only after the worker knows the message settlement path:

- success after processor success before complete
- invalid schema after safe DLQ record succeeds
- permanent failure after safe DLQ record succeeds
- retry exhausted after safe DLQ record succeeds
- abandon on retry cancellation as `worker_abandoned_total{reason=context_canceled}`

- [ ] **Step 5: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/worker`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: record worker observability outcomes"
```

### Task 5: Graph Processor Latency And Classification

**Files:**
- Modify: `internal/graphprocessor/processor.go`
- Modify: `internal/graphprocessor/processor_test.go`

- [ ] **Step 1: Write failing Graph processor observability tests**

Add tests that assert:

```text
success:
  graph_api_latency recorded with labels operation=upsert_password,result=success

permanent graph error:
  graph_api_latency recorded with labels operation=upsert_password,result=permanent_error,status=400

transient graph error:
  graph_api_latency recorded with labels operation=upsert_password,result=transient_error,status=503
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/graphprocessor`

Expected: FAIL because processor has no observability options.

- [ ] **Step 3: Add processor options**

Add:

```go
type Options struct {
	Recorder observability.Recorder
	Now      func() time.Time
}
```

Keep `New(client)` as a wrapper around `NewWithOptions(client, Options{})`.

- [ ] **Step 4: Record duration once per Graph upsert**

Measure around `client.UpsertUserPassword`. Do not log or metric-label the password, display name, mail, token, or request body.

- [ ] **Step 5: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/graphprocessor`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/graphprocessor/processor.go internal/graphprocessor/processor_test.go
git commit -m "feat: record graph processor latency"
```

### Task 6: Queue And Safe DLQ Depth Probes

**Files:**
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/deadletter.go`
- Modify: `internal/servicebusqueue/queue_test.go`

- [ ] **Step 1: Write failing queue depth probe tests**

Add tests around fakes that expose active message counts:

```text
QueueDepth returns active message count for password-sync queue
SafeDLQDepth returns active message count for password-sync-dlq queue
Depth probe errors are returned with operation context
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: FAIL because no depth probe interface exists.

- [ ] **Step 3: Add narrow probe interfaces**

Define interfaces without binding app code directly to Azure Monitor:

```go
type DepthProbe interface {
	ActiveMessageCount(context.Context) (int64, error)
}

type QueueDepthReporter struct {
	probe DepthProbe
	name  string
}
```

Provide methods that call `Recorder.SetGauge(ctx, "queue_depth", count, Labels{"queue": name})` and `Recorder.SetGauge(ctx, "dlq_depth", count, Labels{"queue": name})`.

- [ ] **Step 4: Keep production wiring explicit**

Do not invent Azure management-plane credentials in this slice. Production can wire depth probes later from infrastructure/runtime config. This slice should make the hook point testable and documented.

- [ ] **Step 5: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/servicebusqueue/queue.go internal/servicebusqueue/deadletter.go internal/servicebusqueue/queue_test.go
git commit -m "feat: add service bus depth observability probes"
```

### Task 7: App Wiring And Documentation

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write failing app wiring tests**

Add an app test that constructs app dependencies with a capture recorder and asserts one hook request and one worker message produce expected counters through the wired components.

- [ ] **Step 2: Run app tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: FAIL until app constructors pass recorder/logger options into hook, worker, and graph processor.

- [ ] **Step 3: Add app observability wiring**

Introduce an internal app dependency struct if needed:

```go
type observabilityDeps struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
}
```

Default logger to `slog.Default()` and recorder to `observability.NoopRecorder{}`. Do not change public app behavior.

- [ ] **Step 4: Update README observability section**

Document:

```text
Logs:
- request access logs include traceId, method, path, status, durationMs
- hook outcome logs include action, traceId, cn, upn, outcome
- worker outcome logs include action, traceId, cn, upn, attempts, reason

Metrics:
- hook_requests_total{status,outcome}
- migration_success_total
- migration_failed_total{reason}
- migration_skipped_total{reason}
- queue_depth{queue}
- dlq_depth{queue}
- graph_api_latency{operation,result,status}

Safety:
- passwords, ciphertext, nonce, key IDs, request bodies, Graph tokens, and Graph request bodies are not logged or used as metric labels
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/app ./internal/handler ./internal/worker ./internal/graphprocessor ./internal/servicebusqueue
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go README.md
git commit -m "docs: document observability signals"
```

### Task 8: Final Verification And Promotion Prep

**Files:**
- Modify after promotion only: `docs/superpowers/plans/README.md`
- Modify after promotion only: `docs/superpowers/plans/roadmap.md`

- [ ] **Step 1: Run full tests**

Run: `/usr/local/go/bin/go test -count=1 ./...`

Expected: PASS.

- [ ] **Step 2: Run vet**

Run: `/usr/local/go/bin/go vet ./...`

Expected: PASS.

- [ ] **Step 3: Run leak-focused scans**

Run:

```bash
rg -n "cleartext-password|hook-password|worker-password|passwordCiphertext|passwordNonce|Authorization|Bearer" internal pkg README.md
```

Expected: only test fixtures or documentation describing forbidden fields. No production log or metric label emits secret material.

- [ ] **Step 4: Refresh active plan metadata after Slice 7 merge**

When this draft is promoted, update:

```text
docs/superpowers/plans/README.md
docs/superpowers/plans/roadmap.md
```

Set Slice 8 as the current active detailed plan only after Slice 7 is completed and this plan is moved from `drafts/` to `active/`.

- [ ] **Step 5: Commit promotion metadata**

```bash
git add docs/superpowers/plans/README.md docs/superpowers/plans/roadmap.md
git commit -m "docs: promote slice 8 observability plan"
```

## Draft Self-Review

- Spec coverage: covers structured JSON audit logs, trace IDs, hook/worker success/failure/skip counters, queue depth, safe DLQ depth, and Graph latency from design section 9.
- Scope control: excludes alert rules, dashboards, Azure Monitor exporter implementation, OpenTelemetry dependency selection, and infrastructure provisioning. Those remain later-slice work unless explicitly pulled forward.
- Type consistency: uses one `observability.Recorder` interface and one `observability.Labels` type across hook, worker, graph processor, and queue depth probes.
- Secret safety: labels and logs intentionally exclude passwords, ciphertext, nonce, key IDs, request bodies, Graph tokens, and Graph request bodies.
