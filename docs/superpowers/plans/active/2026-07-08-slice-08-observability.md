# Slice 8 Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add backend-neutral operational logs, counters, durations, queue-depth probes, and trace propagation for hook, migration, worker, Graph, and safe-DLQ paths without recording password material.

**Architecture:** Introduce `internal/observability` as a small application-owned boundary for metric recording, event names, and safe label/attribute helpers. Wire no-op defaults through existing app constructors so production behavior remains unchanged until an exporter is added, while tests can inject capture recorders. Propagate request trace IDs into encrypted queue messages and worker commands so hook, worker, and Graph outcomes can be correlated after Slice 7A event/sync-status decisions.

**Tech Stack:** Go `log/slog`, existing `requestid` context propagation, existing password/secret masking, current in-memory sync-status store, Azure Service Bus adapter boundaries, unit tests with in-memory fakes.

---

## Scope And Constraints

- Keep this slice backend-neutral. Do not add OpenTelemetry, Prometheus, Azure Monitor exporter, Grafana, or cloud-monitoring dependencies.
- Production metrics export is intentionally out of this slice; Azure Monitor export is planned in `docs/superpowers/plans/drafts/2026-07-08-slice-08a-azure-monitor-exporter.md`.
- Do not change password encryption, queue password payload shape beyond adding a non-secret `traceId`, worker retry policy, Graph behavior, safe DLQ payload contents, sync-status state transitions, or API protection behavior.
- Do not include portal/client changes. This slice is limited to password-hook-service observability.
- Do not log or expose cleartext passwords, password ciphertext, password nonce, password key IDs, request bodies, Service Bus message bodies, Graph authorization headers, Graph request bodies, HMAC secrets, nonces, or signatures in logs, labels, metrics, docs examples, or test output.
- Include Slice 7A dimensions in observability: `eventType`, `identityType`, sync-status skip reason (`already_synced`, `sync_pending`), worker result (`synced`, `sync_failed`), safe-DLQ reason, and retry attempts where relevant.
- App wiring must continue to work with existing constructors. New options should be additive and default to `slog.Default()` plus `observability.NoopRecorder{}`.

## Current Context

- `internal/middleware/accesslog.go` already logs request `traceId`, method, path, status, and duration.
- `internal/requestid/requestid.go` carries request IDs through HTTP contexts, but `internal/migration/message.go` does not yet carry a trace ID into queued worker messages.
- `internal/migration/service.go` returns `Decision{IdentityType, UPN, Enqueued, Skipped, Reason}` and now includes Slice 7A event/sync-status skip decisions.
- `internal/handler/hook.go` currently discards the `Decision` from `Submit`, so hook accepted/skipped/error outcomes are not recorded.
- `internal/worker/worker.go` records sync-status success/failure but emits no structured worker outcome events or counters.
- `internal/graphprocessor/processor.go` maps Graph permanent errors to worker permanent errors but emits no operation duration or outcome metric.
- `internal/servicebusqueue/queue.go` and `deadletter.go` send/receive messages but expose no queue-depth probe abstraction.
- `README.md` still says observability is a later slice and needs updating after this plan is implemented.

## File Structure

- Create `internal/observability/recorder.go`: `Recorder`, `Labels`, `NoopRecorder`, and capture recorder used by tests.
- Create `internal/observability/events.go`: metric names, event/action names, safe label helpers, and safe `slog.Attr` helpers.
- Create `internal/observability/recorder_test.go`: no-op, label-copying, and safe helper tests.
- Modify `internal/migration/message.go`: add non-secret `TraceID string` field to password sync messages.
- Modify `internal/migration/service.go`: copy `requestid.From(ctx)` into queued messages.
- Modify `internal/migration/service_test.go` and `message_test.go`: assert trace ID propagation and legacy-message decode compatibility.
- Modify `internal/handler/hook.go`: add hook options and record accepted/skipped/rejected/error outcomes.
- Modify `internal/handler/hook_test.go`: assert metrics/logs include safe fields and exclude password material.
- Modify `internal/middleware/hmac.go`: add observability options and record HMAC authentication rejections.
- Modify `internal/middleware/hmac_test.go`: assert HMAC rejection metrics/logs and no secret/signature/body leakage.
- Modify `internal/middleware/ratelimit.go`: add logger/recorder config and record source allowlist/rate-limit rejections.
- Modify `internal/middleware/ratelimit_test.go`: assert 401/429 middleware metrics/logs.
- Modify `internal/middleware/recovery.go`: add recorder support and record recovered panic outcomes without logging request bodies.
- Modify `internal/middleware/recovery_test.go`: assert recovery metrics/logs.
- Create `internal/middleware/observability.go`: shared safe middleware outcome recorder used by HMAC, rate limiter, and recovery.
- Modify `internal/worker/worker.go`: add logger/recorder options, propagate trace ID into `PasswordSyncCommand`, and record worker receive/process/settlement outcomes.
- Modify `internal/worker/worker_test.go`: assert worker metrics/events for success, invalid message, retry exhaustion, permanent failure, retry-cancel abandon, and safe-DLQ write failure.
- Modify `internal/graphprocessor/processor.go`: add options for recorder/logger/clock and record Graph upsert duration/outcome.
- Modify `internal/graphprocessor/processor_test.go`: assert Graph success/transient/permanent metrics and safe labels.
- Modify `internal/servicebusqueue/queue.go` and `deadletter.go`: add small queue-depth interfaces and adapters without changing send/receive behavior.
- Modify `internal/servicebusqueue/queue_test.go` and `deadletter_test.go`: assert queue-depth probes map active and safe-DLQ counts.
- Modify `internal/app/app.go` and `app_test.go`: wire no-op observability defaults through hook, worker, and Graph processor construction.
- Modify `README.md`: document logs, metric names, labels, queue-depth probes, trace propagation, and sensitive-data exclusions.

---

### Task 1: Observability Recorder Foundation

**Files:**
- Create: `internal/observability/recorder.go`
- Create: `internal/observability/events.go`
- Create: `internal/observability/recorder_test.go`

- [ ] **Step 1: Write failing recorder tests**

Create `internal/observability/recorder_test.go`:

```go
package observability

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestNoopRecorderAcceptsAllSignals(t *testing.T) {
	recorder := NoopRecorder{}

	recorder.Inc(context.Background(), MetricHookRequestsTotal, Labels{"status": "202"})
	recorder.ObserveDuration(context.Background(), MetricGraphUpsertDuration, 12*time.Millisecond, Labels{"outcome": "success"})
	recorder.SetGauge(context.Background(), MetricQueueDepth, 7, Labels{"queue": "password-sync"})
}

func TestCaptureRecorderCopiesLabels(t *testing.T) {
	recorder := NewCaptureRecorder()
	labels := Labels{"status": "202", "outcome": "enqueued"}

	recorder.Inc(context.Background(), MetricHookRequestsTotal, labels)
	labels["status"] = "500"

	got := recorder.Counters(MetricHookRequestsTotal)
	if len(got) != 1 {
		t.Fatalf("counter samples = %d, want 1", len(got))
	}
	if got[0].Labels["status"] != "202" {
		t.Fatalf("stored status = %q, want original 202", got[0].Labels["status"])
	}
}

func TestSafeIdentityAttrsOmitSecrets(t *testing.T) {
	attrs := SafeIdentityAttrs(SafeIdentity{
		TraceID:      "trace-123",
		CN:           "311551001",
		UPN:          "311551001@nycu.edu.tw",
		EventType:    "password_change",
		IdentityType: "student_id",
	})

	got := map[string]any{}
	for _, attr := range attrs {
		got[attr.Key] = attr.Value.Any()
	}
	if got["traceId"] != "trace-123" || got["eventType"] != "password_change" {
		t.Fatalf("attrs = %#v, want traceId and eventType", attrs)
	}
	for _, forbidden := range []string{"password", "passwordCiphertext", "passwordNonce", "passwordKeyId"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("attrs contained forbidden key %q: %#v", forbidden, got)
		}
	}
}

func TestLabelsFromAttrsKeepsStableStringValues(t *testing.T) {
	labels := LabelsFromAttrs([]slog.Attr{
		slog.String("eventType", "login_bootstrap"),
		slog.Int("attempts", 4),
	})

	if labels["eventType"] != "login_bootstrap" || labels["attempts"] != "4" {
		t.Fatalf("labels = %#v, want stringified attr values", labels)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `/usr/local/go/bin/go test ./internal/observability`

Expected: FAIL because `internal/observability` does not exist.

- [ ] **Step 3: Implement recorder types and capture helper**

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

func (r *CaptureRecorder) Durations(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.durations[name]...)
}

func (r *CaptureRecorder) Gauges(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.gauges[name]...)
}

func copyLabels(labels Labels) Labels {
	out := make(Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
```

- [ ] **Step 4: Add metric names, event names, and safe helpers**

Create `internal/observability/events.go`:

```go
package observability

import (
	"fmt"
	"log/slog"
)

const (
	MetricHookRequestsTotal    = "hook_requests_total"
	MetricMigrationSkippedTotal = "migration_skipped_total"
	MetricMiddlewareRequestsTotal = "middleware_requests_total"
	MetricWorkerMessagesTotal  = "worker_messages_total"
	MetricGraphUpsertDuration  = "graph_upsert_duration_seconds"
	MetricQueueDepth           = "queue_depth"

	ActionHookAccepted    = "hook_password_sync_accepted"
	ActionHookSkipped     = "hook_password_sync_skipped"
	ActionHookRejected    = "hook_password_sync_rejected"
	ActionMiddlewareRejected = "middleware_request_rejected"
	ActionMiddlewareRecovered = "middleware_panic_recovered"
	ActionWorkerCompleted = "worker_password_sync_completed"
	ActionWorkerFailed    = "worker_password_sync_failed"
	ActionWorkerInvalid   = "worker_message_invalid"
	ActionWorkerAbandoned = "worker_message_abandoned"
	ActionGraphUpsert     = "graph_password_upsert"
	ActionQueueDepthProbe = "queue_depth_probe"
)

type SafeIdentity struct {
	TraceID      string
	CN           string
	UPN          string
	EventType    string
	IdentityType string
}

func SafeIdentityAttrs(identity SafeIdentity) []slog.Attr {
	attrs := make([]slog.Attr, 0, 5)
	if identity.TraceID != "" {
		attrs = append(attrs, slog.String("traceId", identity.TraceID))
	}
	if identity.CN != "" {
		attrs = append(attrs, slog.String("cn", identity.CN))
	}
	if identity.UPN != "" {
		attrs = append(attrs, slog.String("upn", identity.UPN))
	}
	if identity.EventType != "" {
		attrs = append(attrs, slog.String("eventType", identity.EventType))
	}
	if identity.IdentityType != "" {
		attrs = append(attrs, slog.String("identityType", identity.IdentityType))
	}
	return attrs
}

func LabelsFromAttrs(attrs []slog.Attr) Labels {
	labels := make(Labels, len(attrs))
	for _, attr := range attrs {
		labels[attr.Key] = fmt.Sprint(attr.Value.Any())
	}
	return labels
}
```

- [ ] **Step 5: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/observability`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/observability/recorder.go internal/observability/events.go internal/observability/recorder_test.go
git commit -m "feat: add observability recorder abstraction"
```

### Task 2: Trace ID Propagation Through Queue Messages

**Files:**
- Modify: `internal/migration/message.go`
- Modify: `internal/migration/message_test.go`
- Modify: `internal/migration/service.go`
- Modify: `internal/migration/service_test.go`
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing migration trace tests**

In `internal/migration/service_test.go`, add:

```go
func TestServiceEnqueuedMessageIncludesTraceID(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

	ctx := requestid.With(context.Background(), "trace-123")
	decision, err := service.Submit(ctx, Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queue.messages = %d, want 1", len(queue.messages))
	}
	if queue.messages[0].TraceID != "trace-123" {
		t.Fatalf("queued TraceID = %q, want trace-123", queue.messages[0].TraceID)
	}
}
```

Add `github.com/nycu/password-hook-service/internal/requestid` to the imports.

In `internal/migration/message_test.go`, extend the message JSON round-trip test to set and assert:

```go
TraceID: "trace-123",
```

and:

```go
if decoded.TraceID != "trace-123" {
	t.Errorf("decoded.TraceID = %q, want trace-123", decoded.TraceID)
}
```

Also add a compatibility test for existing messages:

```go
func TestPasswordSyncMessageDecodeAllowsMissingTraceID(t *testing.T) {
	t.Parallel()

	var decoded PasswordSyncMessage
	err := json.Unmarshal([]byte(`{"cn":"311551001","upn":"311551001@nycu.edu.tw","eventType":"login_bootstrap","passwordCiphertext":"ciphertext","passwordNonce":"nonce","passwordKeyId":"password-payload-key-v1","passwordAlg":"AES-256-GCM","displayName":"Student","mail":"student@nycu.edu.tw","enqueuedAt":"2026-07-08T00:00:00Z"}`), &decoded)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.TraceID != "" {
		t.Fatalf("decoded.TraceID = %q, want empty for legacy message", decoded.TraceID)
	}
}
```

- [ ] **Step 2: Write failing worker trace handoff test**

In `internal/worker/worker_test.go`, add:

```go
func TestWorkerPassesTraceIDToProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := validPasswordSyncMessage()
	msg.TraceID = "trace-123"
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{}
	worker := newTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, &fakeDeadLetterSink{})

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if processor.messages[0].TraceID != "trace-123" {
		t.Fatalf("processor TraceID = %q, want trace-123", processor.messages[0].TraceID)
	}
}
```

- [ ] **Step 3: Run tests and verify they fail**

Run:

```bash
/usr/local/go/bin/go test ./internal/migration ./internal/worker
```

Expected: FAIL because `PasswordSyncMessage.TraceID` and `PasswordSyncCommand.TraceID` do not exist.

- [ ] **Step 4: Add non-secret trace fields**

In `internal/migration/message.go`, add:

```go
TraceID string `json:"traceId,omitempty"`
```

In `internal/migration/service.go`, add the import:

```go
"github.com/nycu/password-hook-service/internal/requestid"
```

and set the field when building `PasswordSyncMessage`:

```go
TraceID: requestid.From(ctx),
```

In `internal/worker/worker.go`, add `TraceID string` to `PasswordSyncCommand`, then pass it in `processPasswordSyncAttempt`:

```go
TraceID: msg.TraceID,
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/migration ./internal/worker
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/migration/message.go internal/migration/message_test.go internal/migration/service.go internal/migration/service_test.go internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: propagate trace ids through password sync messages"
```

### Task 3: Hook Outcome Logs And Counters

**Files:**
- Modify: `internal/handler/hook.go`
- Modify: `internal/handler/hook_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing hook instrumentation tests**

In `internal/handler/hook_test.go`, add `log/slog`, `github.com/nycu/password-hook-service/internal/observability`, and a JSON log buffer helper:

```go
func newInstrumentedHook(service *migration.Service, logs *bytes.Buffer, recorder observability.Recorder) *Hook {
	return NewHookWithOptions(service, "https://nycu.edu.tw/problems", HookOptions{
		Logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		Recorder: recorder,
	})
}
```

Add tests:

```go
func TestHookRecordsAcceptedOutcome(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	hook := newInstrumentedHook(service, &logs, recorder)

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"password_change"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	hook.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	samples := recorder.Counters(observability.MetricHookRequestsTotal)
	if len(samples) != 1 {
		t.Fatalf("hook counter samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["status"] != "202" || labels["outcome"] != "enqueued" || labels["eventType"] != "password_change" || labels["identityType"] != "student_id" {
		t.Fatalf("labels = %#v, want accepted hook labels", labels)
	}
	gotLogs := logs.String()
	for _, want := range []string{observability.ActionHookAccepted, `"traceId":"trace-123"`, `"eventType":"password_change"`, `"outcome":"enqueued"`} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs = %s, want %s", gotLogs, want)
		}
	}
	if strings.Contains(gotLogs, "secret") {
		t.Fatalf("logs leaked password: %s", gotLogs)
	}
}

func TestHookRecordsSkippedOutcome(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	hook := newInstrumentedHook(service, &logs, recorder)

	body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com","eventType":"login_bootstrap"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))

	hook.ServeHTTP(rec, req)

	skipped := recorder.Counters(observability.MetricMigrationSkippedTotal)
	if len(skipped) != 1 {
		t.Fatalf("skipped samples = %d, want 1", len(skipped))
	}
	if skipped[0].Labels["reason"] != "cn_is_external_email" || skipped[0].Labels["eventType"] != "login_bootstrap" {
		t.Fatalf("skipped labels = %#v, want external skip reason", skipped[0].Labels)
	}
	if !strings.Contains(logs.String(), observability.ActionHookSkipped) {
		t.Fatalf("logs = %s, want skipped action", logs.String())
	}
}

func TestHookRecordsValidationRejection(t *testing.T) {
	t.Parallel()

	service := migration.NewService("nycu.edu.tw", &captureQueue{}, fakePasswordEncrypter{})
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	hook := newInstrumentedHook(service, &logs, recorder)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", strings.NewReader(`{"password":"secret"}`))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	hook.ServeHTTP(rec, req)

	samples := recorder.Counters(observability.MetricHookRequestsTotal)
	if len(samples) != 1 {
		t.Fatalf("hook counter samples = %d, want 1", len(samples))
	}
	if samples[0].Labels["status"] != "400" || samples[0].Labels["outcome"] != "validation_error" {
		t.Fatalf("labels = %#v, want validation rejection labels", samples[0].Labels)
	}
	if !strings.Contains(logs.String(), observability.ActionHookRejected) || strings.Contains(logs.String(), "secret") {
		t.Fatalf("logs = %s, want rejection action and no password", logs.String())
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/handler`

Expected: FAIL because `NewHookWithOptions`, `HookOptions`, and observability recording do not exist.

- [ ] **Step 3: Add hook options and defaults**

In `internal/handler/hook.go`, add imports for `log/slog` and `internal/observability`. Change the type and constructors:

```go
type Hook struct {
	service        *migration.Service
	problemBaseURL string
	logger         *slog.Logger
	recorder       observability.Recorder
}

type HookOptions struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
}

func NewHook(service *migration.Service, problemBaseURL string) *Hook {
	return NewHookWithOptions(service, problemBaseURL, HookOptions{})
}

func NewHookWithOptions(service *migration.Service, problemBaseURL string, options HookOptions) *Hook {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return &Hook{
		service:        service,
		problemBaseURL: strings.TrimRight(problemBaseURL, "/"),
		logger:         options.Logger,
		recorder:       options.Recorder,
	}
}
```

In `ServeHTTP`, keep the existing validation and error response behavior, but call helpers before each return:

```go
h.recordRejected(r, http.StatusBadRequest, "validation_error", body.EventType)
h.recordRejected(r, http.StatusInternalServerError, "accept_error", body.EventType)
h.recordDecision(r, http.StatusAccepted, body.EventType, decision)
```

Implement helpers that record only safe fields:

```go
func (h *Hook) recordDecision(r *http.Request, status int, eventType migration.EventType, decision migration.Decision) {
	outcome := "accepted"
	if decision.Enqueued {
		outcome = "enqueued"
	}
	if decision.Skipped {
		outcome = "skipped"
	}
	labels := observability.Labels{
		"status":       fmt.Sprint(status),
		"outcome":      outcome,
		"eventType":    string(eventType),
		"identityType": string(decision.IdentityType),
	}
	if decision.Reason != "" {
		labels["reason"] = decision.Reason
	}
	h.recorder.Inc(r.Context(), observability.MetricHookRequestsTotal, labels)
	if decision.Skipped {
		h.recorder.Inc(r.Context(), observability.MetricMigrationSkippedTotal, labels)
	}

	action := observability.ActionHookAccepted
	if decision.Skipped {
		action = observability.ActionHookSkipped
	}
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("outcome", outcome),
		slog.Int("status", status),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID:      requestid.From(r.Context()),
		UPN:          decision.UPN,
		EventType:    string(eventType),
		IdentityType: string(decision.IdentityType),
	})...)
	if decision.Reason != "" {
		attrs = append(attrs, slog.String("reason", decision.Reason))
	}
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, action, attrs...)
}

func (h *Hook) recordRejected(r *http.Request, status int, outcome string, eventType migration.EventType) {
	labels := observability.Labels{"status": fmt.Sprint(status), "outcome": outcome}
	if eventType != "" {
		labels["eventType"] = string(eventType)
	}
	h.recorder.Inc(r.Context(), observability.MetricHookRequestsTotal, labels)
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, observability.ActionHookRejected,
		slog.String("action", observability.ActionHookRejected),
		slog.String("traceId", requestid.From(r.Context())),
		slog.Int("status", status),
		slog.String("outcome", outcome),
	)
}
```

- [ ] **Step 4: Wire app defaults**

In `internal/app/app.go`, replace:

```go
hook := handler.NewHook(service, cfg.ProblemBaseURL)
```

with:

```go
hook := handler.NewHookWithOptions(service, cfg.ProblemBaseURL, handler.HookOptions{
	Logger:   slog.Default(),
	Recorder: observability.NoopRecorder{},
})
```

and add the `internal/observability` import.

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/handler ./internal/app
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/hook.go internal/handler/hook_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: record hook observability outcomes"
```

### Task 4: Middleware Rejection And Recovery Observability

**Files:**
- Modify: `internal/middleware/hmac.go`
- Modify: `internal/middleware/hmac_test.go`
- Modify: `internal/middleware/ratelimit.go`
- Modify: `internal/middleware/ratelimit_test.go`
- Modify: `internal/middleware/recovery.go`
- Modify: `internal/middleware/recovery_test.go`
- Create: `internal/middleware/observability.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing HMAC observability tests**

In `internal/middleware/hmac_test.go`, add imports for `log/slog`, `strings`, `github.com/nycu/password-hook-service/internal/observability`, and `github.com/nycu/password-hook-service/internal/requestid`. Add:

```go
func TestHMACRecordsUnauthorizedRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	middleware, err := NewHMACWithOptions("shared-secret", NewMemoryNonceStore(60*time.Second), 30*time.Second, HMACOptions{
		ProblemBase: "https://nycu.edu.tw/problems",
		Logger:      slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder:    recorder,
	})
	if err != nil {
		t.Fatalf("NewHMACWithOptions returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", strings.NewReader(`{"password":"cleartext-password"}`))
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 {
		t.Fatalf("middleware samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["middleware"] != "hmac" || labels["status"] != "401" || labels["outcome"] != "unauthorized" || labels["reason"] != "missing_or_invalid_signature_headers" {
		t.Fatalf("labels = %#v, want hmac unauthorized labels", labels)
	}
	gotLogs := logs.String()
	for _, want := range []string{observability.ActionMiddlewareRejected, `"middleware":"hmac"`, `"traceId":"trace-123"`, `"status":401`} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs = %s, want %s", gotLogs, want)
		}
	}
	if strings.Contains(gotLogs, "cleartext-password") || strings.Contains(gotLogs, "shared-secret") || strings.Contains(gotLogs, "sha256=") {
		t.Fatalf("logs leaked sensitive data: %s", gotLogs)
	}
}
```

- [ ] **Step 2: Write failing rate-limit and recovery observability tests**

In `internal/middleware/ratelimit_test.go`, add imports for `bytes`, `log/slog`, `strings`, `github.com/nycu/password-hook-service/internal/observability`, and `github.com/nycu/password-hook-service/internal/requestid`. Add:

```go
func TestRateLimiterRecordsSourceAllowlistRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   500,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		Logger:       slog.New(slog.NewJSONHandler(&logs, nil)),
		Recorder:     recorder,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req = req.WithContext(requestid.With(req.Context(), "trace-123"))

	limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 {
		t.Fatalf("middleware samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["middleware"] != "ratelimit" || labels["status"] != "401" || labels["reason"] != "source_ip_not_allowed" {
		t.Fatalf("labels = %#v, want source allowlist rejection", labels)
	}
	if !strings.Contains(logs.String(), observability.ActionMiddlewareRejected) {
		t.Fatalf("logs = %s, want middleware rejection action", logs.String())
	}
}

func TestRateLimiterRecordsThresholdRejection(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	limiter := NewRateLimiter(RateLimitConfig{
		AllowedCIDRs: []string{"192.0.2.0/24"},
		LimitPerIP:   1,
		Window:       time.Second,
		ProblemBase:  "https://nycu.edu.tw/problems",
		Recorder:     recorder,
	})
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	handler.ServeHTTP(second, req)

	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["status"] != "429" || samples[0].Labels["reason"] != "request_rate_exceeded" {
		t.Fatalf("samples = %#v, want rate-limit rejection", samples)
	}
}
```

In `internal/middleware/recovery_test.go`, add `github.com/nycu/password-hook-service/internal/observability` and assert recovered panic metrics:

```go
func TestRecoveryRecordsPanicOutcome(t *testing.T) {
	t.Parallel()

	recorder := observability.NewCaptureRecorder()
	handler := RecoveryWithOptions(RecoveryOptions{
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
		ProblemBase: "https://example.edu/problems",
		Recorder:    recorder,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	samples := recorder.Counters(observability.MetricMiddlewareRequestsTotal)
	if len(samples) != 1 || samples[0].Labels["middleware"] != "recovery" || samples[0].Labels["status"] != "500" || samples[0].Labels["outcome"] != "panic_recovered" {
		t.Fatalf("samples = %#v, want recovery panic metric", samples)
	}
}
```

Add the `io` import for `io.Discard`.

- [ ] **Step 3: Run tests and verify they fail**

Run:

```bash
/usr/local/go/bin/go test ./internal/middleware
```

Expected: FAIL because middleware observability options and metrics do not exist.

- [ ] **Step 4: Add HMAC observability options**

In `internal/middleware/hmac.go`, add imports for `log/slog` and `internal/observability`. Add:

```go
type HMACOptions struct {
	ProblemBase string
	Logger      *slog.Logger
	Recorder    observability.Recorder
}
```

Extend `HMAC`:

```go
logger   *slog.Logger
recorder observability.Recorder
```

Keep existing constructors compatible:

```go
func NewHMACWithProblemBase(secret string, nonces NonceStore, skew time.Duration, problemBase string) (*HMAC, error) {
	return NewHMACWithOptions(secret, nonces, skew, HMACOptions{ProblemBase: problemBase})
}

func NewHMACWithOptions(secret string, nonces NonceStore, skew time.Duration, options HMACOptions) (*HMAC, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("hmac secret is required")
	}
	if skew <= 0 {
		return nil, errors.New("hmac clock skew must be positive")
	}
	if strings.TrimSpace(options.ProblemBase) == "" {
		options.ProblemBase = problem.DefaultBaseURL
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return &HMAC{
		secret:      []byte(secret),
		nonces:      nonces,
		skew:        skew,
		nonceTTL:    nonceTTL(nonces),
		problemBase: options.ProblemBase,
		logger:      options.Logger,
		recorder:    options.Recorder,
	}, nil
}
```

Replace `writeUnauthorized(w, r, detail)` calls with stable reasons:

```go
m.writeUnauthorized(w, r, "failed_to_read_request_body", "failed to read request body")
m.writeUnauthorized(w, r, "missing_or_invalid_signature_headers", "missing or invalid signature headers")
m.writeUnauthorized(w, r, "timestamp_outside_allowed_skew", "signature timestamp is outside allowed skew")
m.writeUnauthorized(w, r, "signature_mismatch", "signature mismatch")
m.writeUnauthorized(w, r, "nonce_replay", "nonce has already been used")
```

Update the helper:

```go
func (m HMAC) writeUnauthorized(w http.ResponseWriter, r *http.Request, reason string, detail string) {
	m.recordMiddlewareRejection(r, "hmac", http.StatusUnauthorized, "unauthorized", reason)
	problem.Write(w, problem.Unauthorized(m.problemBase, r.URL.Path, requestid.From(r.Context()), detail))
}
```

- [ ] **Step 5: Add shared middleware recording helper**

Create `internal/middleware/observability.go`:

```go
package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nycu/password-hook-service/internal/observability"
)

func recordMiddlewareOutcome(ctx context.Context, logger *slog.Logger, recorder observability.Recorder, traceID string, middlewareName string, status int, outcome string, reason string) {
	if recorder == nil {
		recorder = observability.NoopRecorder{}
	}
	labels := observability.Labels{
		"middleware": middlewareName,
		"status":     fmt.Sprint(status),
		"outcome":    outcome,
	}
	if reason != "" {
		labels["reason"] = reason
	}
	recorder.Inc(ctx, observability.MetricMiddlewareRequestsTotal, labels)
	if logger == nil {
		return
	}
	action := observability.ActionMiddlewareRejected
	if outcome == "panic_recovered" {
		action = observability.ActionMiddlewareRecovered
	}
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("traceId", traceID),
		slog.String("middleware", middlewareName),
		slog.Int("status", status),
		slog.String("outcome", outcome),
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, action, attrs...)
}
```

Add an HMAC method:

```go
func (m HMAC) recordMiddlewareRejection(r *http.Request, middlewareName string, status int, outcome string, reason string) {
	recordMiddlewareOutcome(r.Context(), m.logger, m.recorder, requestid.From(r.Context()), middlewareName, status, outcome, reason)
}
```

- [ ] **Step 6: Add rate limiter and recovery options**

In `internal/middleware/ratelimit.go`, add imports for `log/slog` and `internal/observability`. Extend `RateLimitConfig` and `RateLimiter`:

```go
Logger   *slog.Logger
Recorder observability.Recorder
```

Default them in `NewRateLimiter`:

```go
if cfg.Logger == nil {
	cfg.Logger = slog.Default()
}
if cfg.Recorder == nil {
	cfg.Recorder = observability.NoopRecorder{}
}
```

Assign them in the returned limiter:

```go
return &RateLimiter{
	allowedCIDRs: parseCIDRs(cfg.AllowedCIDRs),
	limitPerIP:   cfg.LimitPerIP,
	window:       cfg.Window,
	problemBase:  cfg.ProblemBase,
	logger:       cfg.Logger,
	recorder:     cfg.Recorder,
	counts:       map[string]rateWindow{},
}
```

Before writing source allowlist and threshold problems, record:

```go
recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusUnauthorized, "unauthorized", "source_ip_not_allowed")
recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusTooManyRequests, "rate_limited", "request_rate_exceeded")
```

In `internal/middleware/recovery.go`, add imports for `strings` and `internal/observability`, then replace the current options shape with an additive constructor:

```go
type RecoveryOptions struct {
	Logger      *slog.Logger
	ProblemBase string
	Recorder    observability.Recorder
}

func RecoveryWithOptions(options RecoveryOptions) func(http.Handler) http.Handler {
	if strings.TrimSpace(options.ProblemBase) == "" {
		options.ProblemBase = problem.DefaultBaseURL
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if options.Logger != nil {
						options.Logger.Error("panic recovered", slog.Any("panic", recovered))
					}
					recordMiddlewareOutcome(r.Context(), options.Logger, options.Recorder, requestid.From(r.Context()), "recovery", http.StatusInternalServerError, "panic_recovered", "panic")
					problem.Write(w, problem.Internal(options.ProblemBase, r.URL.Path, requestid.From(r.Context()), "unexpected server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
```

Keep existing constructors by delegating:

```go
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return RecoveryWithOptions(RecoveryOptions{Logger: log, ProblemBase: problem.DefaultBaseURL})
}

func RecoveryWithProblemBase(log *slog.Logger, problemBase string) func(http.Handler) http.Handler {
	return RecoveryWithOptions(RecoveryOptions{Logger: log, ProblemBase: problemBase})
}
```

- [ ] **Step 7: Wire middleware no-op defaults through app**

In `internal/app/app.go`, change middleware construction:

```go
hmacMiddleware, err := middleware.NewHMACWithOptions(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, middleware.HMACOptions{
	ProblemBase: cfg.ProblemBaseURL,
	Logger:      slog.Default(),
	Recorder:    observability.NoopRecorder{},
})
```

and:

```go
rateLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
	AllowedCIDRs: cfg.PortalAllowedCIDRs,
	LimitPerIP:   cfg.RateLimitPerIP,
	Window:       cfg.RateLimitWindow,
	ProblemBase:  cfg.ProblemBaseURL,
	Logger:       slog.Default(),
	Recorder:     observability.NoopRecorder{},
})
```

and:

```go
hookHandler = middleware.RecoveryWithOptions(middleware.RecoveryOptions{
	Logger:      slog.Default(),
	ProblemBase: cfg.ProblemBaseURL,
	Recorder:    observability.NoopRecorder{},
})(hookHandler)
```

- [ ] **Step 8: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/middleware ./internal/app
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/middleware/hmac.go internal/middleware/hmac_test.go internal/middleware/ratelimit.go internal/middleware/ratelimit_test.go internal/middleware/recovery.go internal/middleware/recovery_test.go internal/middleware/observability.go internal/app/app.go
git commit -m "feat: record middleware observability outcomes"
```

### Task 5: Worker Outcome Logs And Counters

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing worker observability tests**

In `internal/worker/worker_test.go`, add imports for `bytes`, `log/slog`, and `internal/observability`. Add a helper:

```go
func newObservableTestWorker(t *testing.T, receiver Receiver, processor Processor, decrypter PasswordDecrypter, deadLetters DeadLetterSink, recorder observability.Recorder, logs *bytes.Buffer) *Worker {
	t.Helper()
	worker, err := New(receiver, processor, Options{
		MaxMessages:        10,
		DeadLetterSink:     deadLetters,
		PasswordDecrypter:  decrypter,
		SyncStatusRecorder: &fakeSyncStatusRecorder{},
		Recorder:           recorder,
		Logger:             slog.New(slog.NewJSONHandler(logs, nil)),
		Sleep:              (&fakeSleeper{}).Sleep,
		Now:                func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return worker
}
```

Add success, invalid, and terminal-failure tests:

```go
func TestWorkerRecordsSuccessOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := validPasswordSyncMessage()
	msg.TraceID = "trace-123"
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onComplete = cancel
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	worker := newObservableTestWorker(t, receiver, &fakeProcessor{}, &fakePasswordDecrypter{plaintext: []byte("cleartext-password")}, &fakeDeadLetterSink{}, recorder, &logs)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	samples := recorder.Counters(observability.MetricWorkerMessagesTotal)
	if len(samples) != 1 {
		t.Fatalf("worker message samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["outcome"] != "synced" || labels["eventType"] != string(msg.EventType) {
		t.Fatalf("labels = %#v, want synced event labels", labels)
	}
	gotLogs := logs.String()
	for _, want := range []string{observability.ActionWorkerCompleted, `"traceId":"trace-123"`, `"outcome":"synced"`} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs = %s, want %s", gotLogs, want)
		}
	}
	if strings.Contains(gotLogs, "cleartext-password") || strings.Contains(gotLogs, msg.PasswordCiphertext) {
		t.Fatalf("logs leaked password material: %s", gotLogs)
	}
}

func TestWorkerRecordsInvalidMessageOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{{Kind: passwordSyncKind, Body: []byte(`{"cn":"311551001","upn":"311551001@nycu.edu.tw"}`)}}}
	receiver.onComplete = cancel
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	worker := newObservableTestWorker(t, receiver, &fakeProcessor{}, &fakePasswordDecrypter{}, &fakeDeadLetterSink{}, recorder, &logs)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	samples := recorder.Counters(observability.MetricWorkerMessagesTotal)
	if len(samples) != 1 || samples[0].Labels["outcome"] != "invalid_message" || samples[0].Labels["reason"] != DeadLetterReasonInvalidMessageSchema {
		t.Fatalf("samples = %#v, want invalid message labels", samples)
	}
	if !strings.Contains(logs.String(), observability.ActionWorkerInvalid) {
		t.Fatalf("logs = %s, want invalid action", logs.String())
	}
}

func TestWorkerRecordsTerminalFailureOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := validPasswordSyncMessage()
	msg.EventType = migration.EventPasswordRecovery
	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, msg)}}
	receiver.onComplete = cancel
	processor := &fakeProcessor{err: &PermanentError{Reason: PermanentReasonProcessorError, Err: errors.New("graph 403")}}
	deadLetters := &fakeDeadLetterSink{onRecord: func() {}}
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	worker := newObservableTestWorker(t, receiver, processor, &fakePasswordDecrypter{plaintext: []byte("secret")}, deadLetters, recorder, &logs)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	samples := recorder.Counters(observability.MetricWorkerMessagesTotal)
	if len(samples) != 1 {
		t.Fatalf("worker samples = %d, want 1", len(samples))
	}
	labels := samples[0].Labels
	if labels["outcome"] != "sync_failed" || labels["reason"] != DeadLetterReasonPermanentProcessor || labels["attempts"] != "1" || labels["eventType"] != "password_recovery" {
		t.Fatalf("labels = %#v, want terminal failure labels", labels)
	}
	if !strings.Contains(logs.String(), observability.ActionWorkerFailed) || strings.Contains(logs.String(), "secret") {
		t.Fatalf("logs = %s, want safe failed action", logs.String())
	}
}
```

Also add a focused test for retry-cancel abandon:

```go
func TestWorkerRecordsRetryCancelAbandonOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := &fakeReceiver{messages: []*Message{workerMessage(t, validPasswordSyncMessage())}}
	receiver.onAbandon = cancel
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	worker, err := New(receiver, &fakeProcessor{err: errors.New("retryable")}, Options{
		MaxMessages:        10,
		DeadLetterSink:     &fakeDeadLetterSink{},
		PasswordDecrypter:  &fakePasswordDecrypter{plaintext: []byte("secret")},
		SyncStatusRecorder: &fakeSyncStatusRecorder{},
		Recorder:           recorder,
		Logger:             slog.New(slog.NewJSONHandler(&logs, nil)),
		Sleep:              (&fakeSleeper{err: context.Canceled}).Sleep,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	samples := recorder.Counters(observability.MetricWorkerMessagesTotal)
	if len(samples) != 1 || samples[0].Labels["outcome"] != "abandoned" {
		t.Fatalf("samples = %#v, want abandoned outcome", samples)
	}
	if !strings.Contains(logs.String(), observability.ActionWorkerAbandoned) {
		t.Fatalf("logs = %s, want abandoned action", logs.String())
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/worker`

Expected: FAIL because worker options do not include logger/recorder and no outcomes are recorded.

- [ ] **Step 3: Add worker options and defaults**

In `internal/worker/worker.go`, add imports for `log/slog` and `internal/observability`.

Extend `Options`:

```go
Logger   *slog.Logger
Recorder observability.Recorder
```

Extend `Worker`:

```go
logger   *slog.Logger
recorder observability.Recorder
```

In `New`, default missing values:

```go
if options.Logger == nil {
	options.Logger = slog.Default()
}
if options.Recorder == nil {
	options.Recorder = observability.NoopRecorder{}
}
```

and assign them in the returned `Worker`.

- [ ] **Step 4: Record safe outcomes from existing branches**

Add a helper near `processMessage`:

```go
func (w *Worker) recordOutcome(ctx context.Context, action string, msg migration.PasswordSyncMessage, outcome string, reason string, attempts int) {
	labels := observability.Labels{
		"outcome":   outcome,
		"eventType": string(msg.EventType),
	}
	if reason != "" {
		labels["reason"] = reason
	}
	if attempts > 0 {
		labels["attempts"] = fmt.Sprint(attempts)
	}
	w.recorder.Inc(ctx, observability.MetricWorkerMessagesTotal, labels)
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("outcome", outcome),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID:   msg.TraceID,
		CN:        msg.CN,
		UPN:       msg.UPN,
		EventType: string(msg.EventType),
	})...)
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	if attempts > 0 {
		attrs = append(attrs, slog.Int("attempts", attempts))
	}
	w.logger.LogAttrs(ctx, slog.LevelInfo, action, attrs...)
}
```

For invalid messages, create a safe partial-message helper:

```go
func invalidMessageForObservability(entry DeadLetterEntry) migration.PasswordSyncMessage {
	return migration.PasswordSyncMessage{CN: entry.CN, UPN: entry.UPN, EnqueuedAt: entry.EnqueuedAt}
}
```

Call `recordOutcome` after the irreversible outcome is known:

- after invalid safe DLQ record and complete succeeds: outcome `invalid_message`, reason `invalid_message_schema`
- after successful complete succeeds: outcome `synced`
- after retry-cancel abandon succeeds: outcome `abandoned`
- after terminal safe DLQ record, `MarkFailed`, and complete succeeds: outcome `sync_failed`, reason set to the safe DLQ reason, attempts set to `result.attempts`

Do not log or label `PasswordCiphertext`, `PasswordNonce`, `PasswordKeyID`, `PasswordAlg`, plaintext password, raw message body, or processor error strings.

- [ ] **Step 5: Wire app worker defaults**

In `internal/app/app.go`, pass no-op observability options when constructing the worker:

```go
Logger:   slog.Default(),
Recorder: observability.NoopRecorder{},
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/worker ./internal/app
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go internal/app/app.go
git commit -m "feat: record worker observability outcomes"
```

### Task 6: Graph Processor Duration And Outcome Metrics

**Files:**
- Modify: `internal/graphprocessor/processor.go`
- Modify: `internal/graphprocessor/processor_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing Graph processor observability tests**

In `internal/graphprocessor/processor_test.go`, add imports for `bytes`, `log/slog`, `strings`, `time`, and `internal/observability`. Add:

```go
func TestProcessorRecordsGraphSuccessDuration(t *testing.T) {
	recorder := observability.NewCaptureRecorder()
	var logs bytes.Buffer
	processor, err := NewWithOptions(&captureGraphClient{}, Options{
		Recorder: recorder,
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Now:      fixedClock(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), time.Date(2026, 7, 8, 12, 0, 0, 25*int(time.Millisecond), time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}

	err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
		TraceID:     "trace-123",
		UPN:         "311551001@nycu.edu.tw",
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
		Password:    []byte("cleartext-password"),
	})
	if err != nil {
		t.Fatalf("ProcessPasswordSync returned error: %v", err)
	}

	samples := recorder.Durations(observability.MetricGraphUpsertDuration)
	if len(samples) != 1 {
		t.Fatalf("duration samples = %d, want 1", len(samples))
	}
	if samples[0].Labels["outcome"] != "success" {
		t.Fatalf("labels = %#v, want success", samples[0].Labels)
	}
	if samples[0].Duration != 25*time.Millisecond {
		t.Fatalf("duration = %s, want 25ms", samples[0].Duration)
	}
	gotLogs := logs.String()
	if !strings.Contains(gotLogs, observability.ActionGraphUpsert) || !strings.Contains(gotLogs, `"traceId":"trace-123"`) {
		t.Fatalf("logs = %s, want graph upsert with traceId", gotLogs)
	}
	if strings.Contains(gotLogs, "cleartext-password") {
		t.Fatalf("logs leaked password: %s", gotLogs)
	}
}

func TestProcessorRecordsGraphPermanentAndTransientOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantLabel  string
		wantWorker bool
	}{
		{
			name:       "permanent",
			err:        &graphclient.PermanentError{StatusCode: 403, Operation: "patch user"},
			wantLabel:  "permanent_error",
			wantWorker: true,
		},
		{
			name:      "transient",
			err:       &graphclient.TransientError{StatusCode: 503, Operation: "patch user"},
			wantLabel: "transient_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := observability.NewCaptureRecorder()
			processor, err := NewWithOptions(&captureGraphClient{err: tt.err}, Options{Recorder: recorder})
			if err != nil {
				t.Fatalf("NewWithOptions returned error: %v", err)
			}

			err = processor.ProcessPasswordSync(context.Background(), worker.PasswordSyncCommand{
				UPN:      "311551001@nycu.edu.tw",
				Password: []byte("cleartext-password"),
			})

			var permanent *worker.PermanentError
			if gotPermanent := errors.As(err, &permanent); gotPermanent != tt.wantWorker {
				t.Fatalf("worker permanent = %v, want %v; err=%v", gotPermanent, tt.wantWorker, err)
			}
			samples := recorder.Durations(observability.MetricGraphUpsertDuration)
			if len(samples) != 1 || samples[0].Labels["outcome"] != tt.wantLabel {
				t.Fatalf("samples = %#v, want outcome %s", samples, tt.wantLabel)
			}
		})
	}
}

func fixedClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		if i >= len(times) {
			return times[len(times)-1]
		}
		got := times[i]
		i++
		return got
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/graphprocessor`

Expected: FAIL because `NewWithOptions` and `Options` do not exist.

- [ ] **Step 3: Add processor options and defaults**

In `internal/graphprocessor/processor.go`, add imports for `log/slog`, `time`, and `internal/observability`. Change the type:

```go
type Processor struct {
	client   graphclient.Client
	logger   *slog.Logger
	recorder observability.Recorder
	now      func() time.Time
}

type Options struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
	Now      func() time.Time
}
```

Keep the existing constructor and add an options constructor:

```go
func New(client graphclient.Client) (*Processor, error) {
	return NewWithOptions(client, Options{})
}

func NewWithOptions(client graphclient.Client, options Options) (*Processor, error) {
	if client == nil {
		return nil, errors.New("graph client is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Processor{client: client, logger: options.Logger, recorder: options.Recorder, now: options.Now}, nil
}
```

- [ ] **Step 4: Record duration and safe outcome**

Wrap the existing Graph call:

```go
func (p *Processor) ProcessPasswordSync(ctx context.Context, msg worker.PasswordSyncCommand) error {
	start := p.now()
	err := p.client.UpsertUserPassword(ctx, graphclient.User{
		UPN:         msg.UPN,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
	}, msg.Password)

	outcome := "success"
	if err != nil {
		outcome = "transient_error"
		var permanent *graphclient.PermanentError
		if errors.As(err, &permanent) {
			outcome = "permanent_error"
		}
	}
	duration := p.now().Sub(start)
	labels := observability.Labels{"outcome": outcome}
	p.recorder.ObserveDuration(ctx, observability.MetricGraphUpsertDuration, duration, labels)
	attrs := []slog.Attr{
		slog.String("action", observability.ActionGraphUpsert),
		slog.String("outcome", outcome),
		slog.Int64("durationMs", duration.Milliseconds()),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID: msg.TraceID,
		UPN:     msg.UPN,
	})...)
	p.logger.LogAttrs(ctx, slog.LevelInfo, observability.ActionGraphUpsert, attrs...)

	if err == nil {
		return nil
	}
	var permanent *graphclient.PermanentError
	if errors.As(err, &permanent) {
		return &worker.PermanentError{Reason: worker.PermanentReasonProcessorError, Err: permanent}
	}
	return err
}
```

Do not log the Graph error string because it may contain provider response details.

- [ ] **Step 5: Wire app default Graph observability**

In `internal/app/app.go`, replace:

```go
processor, err := graphprocessor.New(graph)
```

with:

```go
processor, err := graphprocessor.NewWithOptions(graph, graphprocessor.Options{
	Logger:   slog.Default(),
	Recorder: observability.NoopRecorder{},
})
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/graphprocessor ./internal/app
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/graphprocessor/processor.go internal/graphprocessor/processor_test.go internal/app/app.go
git commit -m "feat: record graph processor observability"
```

### Task 7: Queue Depth Probe Boundary And Documentation

**Files:**
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/deadletter.go`
- Modify: `internal/servicebusqueue/queue_test.go`
- Modify: `internal/servicebusqueue/deadletter_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write failing queue-depth probe tests**

In `internal/servicebusqueue/queue_test.go`, add:

```go
func TestQueueDepthProbeRecordsActiveQueueDepth(t *testing.T) {
	t.Parallel()

	reader := &fakeDepthReader{depths: map[string]int64{"password-sync": 12}}
	recorder := observability.NewCaptureRecorder()
	probe := NewQueueDepthProbe(reader, QueueDepthProbeOptions{
		QueueName: "password-sync",
		Kind:      "active",
		Recorder:  recorder,
	})

	depth, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if depth != 12 {
		t.Fatalf("depth = %d, want 12", depth)
	}
	samples := recorder.Gauges(observability.MetricQueueDepth)
	if len(samples) != 1 {
		t.Fatalf("queue depth samples = %d, want 1", len(samples))
	}
	if samples[0].Value != 12 || samples[0].Labels["queue"] != "password-sync" || samples[0].Labels["kind"] != "active" {
		t.Fatalf("sample = %#v, want active queue depth labels", samples[0])
	}
}

type fakeDepthReader struct {
	depths map[string]int64
	err    error
}

func (f *fakeDepthReader) ActiveMessageCount(_ context.Context, queueName string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.depths[queueName], nil
}
```

Add the `internal/observability` import.

In `internal/servicebusqueue/deadletter_test.go`, add:

```go
func TestSafeDLQDepthProbeRecordsDepth(t *testing.T) {
	t.Parallel()

	reader := &fakeDepthReader{depths: map[string]int64{"password-sync-dlq": 3}}
	recorder := observability.NewCaptureRecorder()
	probe := NewSafeDLQDepthProbe(reader, "password-sync-dlq", recorder)

	depth, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if depth != 3 {
		t.Fatalf("depth = %d, want 3", depth)
	}
	samples := recorder.Gauges(observability.MetricQueueDepth)
	if len(samples) != 1 || samples[0].Labels["kind"] != "safe_dlq" {
		t.Fatalf("samples = %#v, want safe_dlq queue depth", samples)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: FAIL because depth probe types do not exist.

- [ ] **Step 3: Add queue-depth probe abstraction**

In `internal/servicebusqueue/queue.go`, add imports for `strings` and `internal/observability` if they are not already present. Add:

```go
type QueueDepthReader interface {
	ActiveMessageCount(context.Context, string) (int64, error)
}

type QueueDepthProbeOptions struct {
	QueueName string
	Kind      string
	Recorder  observability.Recorder
}

type QueueDepthProbe struct {
	reader   QueueDepthReader
	queue    string
	kind     string
	recorder observability.Recorder
}

func NewQueueDepthProbe(reader QueueDepthReader, options QueueDepthProbeOptions) *QueueDepthProbe {
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return &QueueDepthProbe{
		reader:   reader,
		queue:    strings.TrimSpace(options.QueueName),
		kind:     strings.TrimSpace(options.Kind),
		recorder: options.Recorder,
	}
}

func (p *QueueDepthProbe) Probe(ctx context.Context) (int64, error) {
	if p == nil || p.reader == nil {
		return 0, errors.New("queue depth reader is required")
	}
	if p.queue == "" {
		return 0, errors.New("queue depth queue name is required")
	}
	depth, err := p.reader.ActiveMessageCount(ctx, p.queue)
	if err != nil {
		return 0, fmt.Errorf("read queue depth: %w", err)
	}
	kind := p.kind
	if kind == "" {
		kind = "active"
	}
	p.recorder.SetGauge(ctx, observability.MetricQueueDepth, depth, observability.Labels{
		"queue": p.queue,
		"kind":  kind,
	})
	return depth, nil
}
```

This intentionally defines only the application boundary. An Azure Monitor or Service Bus management implementation can be added later without changing hook/worker business logic.

- [ ] **Step 4: Add safe-DLQ probe helper**

In `internal/servicebusqueue/deadletter.go`, add:

```go
func NewSafeDLQDepthProbe(reader QueueDepthReader, queueName string, recorder observability.Recorder) *QueueDepthProbe {
	return NewQueueDepthProbe(reader, QueueDepthProbeOptions{
		QueueName: queueName,
		Kind:      "safe_dlq",
		Recorder:  recorder,
	})
}
```

Add the `internal/observability` import.

- [ ] **Step 5: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: PASS.

- [ ] **Step 6: Update README observability docs**

In `README.md`, update Current Scope to remove “observability” from the later-slices sentence and add bullets for:

```markdown
- backend-neutral observability recorder boundary
- structured hook, worker, and Graph outcome events
- trace ID propagation through queue messages
- queue and safe-DLQ depth probe boundaries
```

Add a new `## Observability` section before `## Configuration`:

```markdown
## Observability

The service emits structured JSON logs through `log/slog` and records metrics through the backend-neutral `internal/observability.Recorder` interface. Production currently wires a no-op recorder; exporters to Azure Monitor, OpenTelemetry, Prometheus, or another backend should be adapters over that interface.

Key structured actions:

| Action | Meaning |
|--------|---------|
| `hook_password_sync_accepted` | A hook request was accepted and enqueued. |
| `hook_password_sync_skipped` | A hook request was accepted but skipped, for example external email identity or Slice 7A sync-status dedupe. |
| `hook_password_sync_rejected` | A hook request failed validation or acceptance. |
| `middleware_request_rejected` | HMAC, source allowlist, or rate-limit middleware rejected a request before it reached the hook. |
| `middleware_panic_recovered` | Recovery middleware handled a panic and returned an RFC 9457 500 response. |
| `worker_password_sync_completed` | The worker completed a password sync and marked the account synced. |
| `worker_password_sync_failed` | The worker recorded a terminal safe-DLQ outcome and marked sync failed. |
| `worker_message_invalid` | The worker completed an invalid queue message after writing a password-safe DLQ record. |
| `worker_message_abandoned` | The worker abandoned a message because retry backoff was canceled or safe-DLQ recording failed. |
| `graph_password_upsert` | The Graph processor attempted a create/update password operation. |

Metric names:

| Metric | Type | Labels |
|--------|------|--------|
| `hook_requests_total` | counter | `status`, `outcome`, `eventType`, `identityType`, optional `reason` |
| `migration_skipped_total` | counter | `outcome`, `eventType`, `identityType`, `reason` |
| `middleware_requests_total` | counter | `middleware`, `status`, `outcome`, optional `reason` |
| `worker_messages_total` | counter | `outcome`, `eventType`, optional `reason`, optional `attempts` |
| `graph_upsert_duration_seconds` | duration | `outcome` |
| `queue_depth` | gauge | `queue`, `kind` |

Logs and metric labels must not include cleartext passwords, encrypted password fields, request bodies, Service Bus message bodies, Graph request bodies, HMAC secrets, HMAC signatures, nonces, or authorization headers.
```

- [ ] **Step 7: Run docs and package verification**

Run:

```bash
/usr/local/go/bin/go test ./internal/servicebusqueue ./internal/observability
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/servicebusqueue/queue.go internal/servicebusqueue/deadletter.go internal/servicebusqueue/queue_test.go internal/servicebusqueue/deadletter_test.go README.md
git commit -m "feat: add queue depth observability hooks"
```

### Task 8: Full Verification And Leak Scan

**Files:**
- No source files unless verification exposes a defect in this slice.

- [ ] **Step 1: Run formatter, tests, and vet**

Run:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Run leak-focused scans**

Run:

```bash
rg -n "cleartext-password|must-not-appear|passwordCiphertext|passwordNonce|passwordKeyId|X-Hook-Signature|Authorization" internal README.md
```

Expected: only test fixtures, schema names, README forbidden-field documentation, and existing safe assertions appear. No observability log/label implementation should emit these values.

- [ ] **Step 3: Run standard project verification**

Run:

```bash
make verify
```

Expected: PASS.

- [ ] **Step 4: Commit verification fixes if needed**

If verification required fixes, commit them:

```bash
git add internal/observability internal/migration internal/handler internal/middleware internal/worker internal/graphprocessor internal/servicebusqueue internal/app README.md
git commit -m "fix: address observability verification issues"
```

If no fixes were needed, do not create an empty commit.
