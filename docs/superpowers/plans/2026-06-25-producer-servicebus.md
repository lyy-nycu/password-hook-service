# Producer to Service Bus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `POST /api/v1/hook/password` enqueue eligible internal password sync jobs to Azure Service Bus with a 300 second message TTL while continuing to skip external email identities without enqueueing.

**Architecture:** Keep identity policy in `internal/migration` and add a narrow `internal/servicebusqueue` adapter that implements `migration.Queue`. Production `app.New` builds an Azure Service Bus sender from environment config; tests use `app.NewWithQueue` and fake senders to avoid network calls. Slice 2 does not implement the worker, Graph client, Key Vault loading, retry/DLQ policy, or password zeroing beyond preserving the no-log guarantee.

**Tech Stack:** Go 1.26, `github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus`, `net/http`, Dockerized Go toolchain, minimal Makefile wrapper for local verification.

**Reference APIs verified on 2026-06-25:**
- `github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus` is the Azure Service Bus client module for Go.
- Use `azservicebus.NewClientFromConnectionString(connectionString, nil)` for Slice 2 local/runtime config.
- Use `client.NewSender(queueName, nil)` and `sender.SendMessage(ctx, message, nil)` to send.
- Use `azservicebus.Message.TimeToLive` to set the 300 second TTL.

---

## Makefile Evaluation

Decision: include a small Makefile in Slice 2.

Reasoning: this project already relies on long Dockerized Go commands in README and during review. Slice 2 introduces the first external Go module, so repeated `go test`, `go vet`, `gofmt`, and Docker build commands become common enough to justify a small wrapper. The Makefile must stay local-development focused and must not become a deployment system.

Accepted targets:
- `make fmt`
- `make test`
- `make vet`
- `make verify`
- `make docker-build`

Rejected for this slice:
- Terraform targets
- Azure login/deploy targets
- Secret management targets
- CI scanner orchestration

---

## File Structure

- Create: `Makefile` - local Dockerized Go command wrapper.
- Modify: `README.md` - document Makefile, Service Bus producer config, and Slice 2 local verification.
- Modify: `go.mod` and create/update `go.sum` - add Azure Service Bus SDK dependency.
- Modify: `internal/config/config.go` - add `SERVICEBUS_CONNECTION_STRING`, `SERVICEBUS_QUEUE_NAME`, and fixed `PasswordMessageTTL` config.
- Modify: `internal/config/config_test.go` - cover required Service Bus config and TTL validation.
- Create: `internal/servicebusqueue/queue.go` - Azure Service Bus `migration.Queue` implementation.
- Create: `internal/servicebusqueue/queue_test.go` - test JSON body, TTL, metadata, error propagation, and close behavior with fake sender.
- Modify: `internal/app/app.go` - wire production Service Bus queue and add test injection via `NewWithQueue`.
- Modify: `internal/app/app_test.go` - use injected fake queue and verify internal enqueue, external skip, and no password logs through the real HTTP path.
- Modify: `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md` - mark Slice 2 as planned/active during implementation and done after verification.

---

### Task 1: Add Minimal Makefile

**Files:**
- Create: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Write the Makefile**

Create `Makefile`:

```makefile
GO_IMAGE ?= golang:1.26.4
DOCKER_RUN = docker run --rm -v "$(PWD):/src" -w /src $(GO_IMAGE)

.PHONY: fmt test vet verify docker-build

fmt:
	$(DOCKER_RUN) gofmt -w .

test:
	$(DOCKER_RUN) go test ./...

vet:
	$(DOCKER_RUN) go vet ./...

verify:
	$(DOCKER_RUN) sh -c "gofmt -w . && go test ./... && go vet ./..."

docker-build:
	docker build -f deploy/Dockerfile -t password-hook-service .
```

- [ ] **Step 2: Run Makefile targets**

Run:

```bash
make test
make vet
```

Expected: both commands pass. `make test` may print `go: no module dependencies to download` before Slice 2 dependency is added.

- [ ] **Step 3: Update README verification commands**

In `README.md`, replace the long local verification commands with Makefile-first instructions while keeping the raw Docker command visible as fallback:

````markdown
## Local Verification

Run the standard local verification:

```bash
make verify
```

The Makefile wraps the Dockerized Go toolchain. The equivalent raw command is:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."
```
````

- [ ] **Step 4: Commit**

```bash
git add Makefile README.md
git commit -m "chore: add local verification make targets"
```

---

### Task 2: Add Service Bus Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Append to `internal/config/config_test.go`:

```go
func TestValidateRequiresServiceBusConnectionString(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusConnectionString = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without SERVICEBUS_CONNECTION_STRING")
	}
}

func TestValidateRequiresServiceBusQueueName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusQueueName = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without SERVICEBUS_QUEUE_NAME")
	}
}

func TestValidateRequiresPositivePasswordMessageTTL(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PasswordMessageTTL = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without positive PasswordMessageTTL")
	}
}

func completeConfig() Config {
	return Config{
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		PortalAllowedCIDRs:         nil,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
}
```

Update the existing config tests to call `completeConfig()` and then override only the field under test.

- [ ] **Step 2: Run config tests to verify failure**

Run:

```bash
make test
```

Expected: FAIL in `internal/config` because `Config` does not yet have `ServiceBusConnectionString`, `ServiceBusQueueName`, or `PasswordMessageTTL`.

- [ ] **Step 3: Implement config fields and validation**

In `internal/config/config.go`, add fields:

```go
ServiceBusConnectionString string
ServiceBusQueueName        string
PasswordMessageTTL         time.Duration
```

Update `Load()`:

```go
ServiceBusConnectionString: strings.TrimSpace(os.Getenv("SERVICEBUS_CONNECTION_STRING")),
ServiceBusQueueName:        env("SERVICEBUS_QUEUE_NAME", "password-sync"),
PasswordMessageTTL:         300 * time.Second,
```

Update `Validate()` before the CIDR validation:

```go
case strings.TrimSpace(c.ServiceBusConnectionString) == "":
	return errors.New("SERVICEBUS_CONNECTION_STRING is required")
case strings.TrimSpace(c.ServiceBusQueueName) == "":
	return errors.New("SERVICEBUS_QUEUE_NAME is required")
case c.PasswordMessageTTL <= 0:
	return errors.New("PasswordMessageTTL must be positive")
```

- [ ] **Step 4: Run focused config tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/config
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: require service bus producer configuration"
```

---

### Task 3: Add Service Bus Queue Adapter

**Files:**
- Modify: `go.mod`
- Create/update: `go.sum`
- Create: `internal/servicebusqueue/queue.go`
- Create: `internal/servicebusqueue/queue_test.go`

- [ ] **Step 1: Add Azure Service Bus dependency**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go get github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus
```

Expected: `go.mod` gains the Azure SDK modules needed by `azservicebus`, and `go.sum` is created or updated.

- [ ] **Step 2: Write failing queue adapter tests**

Create `internal/servicebusqueue/queue_test.go`:

```go
package servicebusqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
)

func TestQueueSendsPasswordSyncMessageWithTTL(t *testing.T) {
	t.Parallel()

	sender := &captureSender{}
	queue := New(sender, 300*time.Second)
	msg := migration.PasswordSyncMessage{
		CN:          "311551001",
		UPN:         "311551001@nycu.edu.tw",
		Password:    "cleartext-password",
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
		EnqueuedAt:  time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC),
	}

	if err := queue.EnqueuePasswordSync(context.Background(), msg); err != nil {
		t.Fatalf("EnqueuePasswordSync returned error: %v", err)
	}

	if len(sender.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.messages))
	}
	got := sender.messages[0]
	if got.TimeToLive == nil || *got.TimeToLive != 300*time.Second {
		t.Fatalf("TimeToLive = %v, want 300s", got.TimeToLive)
	}
	if got.ContentType == nil || *got.ContentType != "application/json" {
		t.Fatalf("ContentType = %v, want application/json", got.ContentType)
	}
	if got.MessageID == nil || *got.MessageID == "" {
		t.Fatal("MessageID was empty")
	}

	var payload migration.PasswordSyncMessage
	if err := json.Unmarshal(got.Body, &payload); err != nil {
		t.Fatalf("message body is not PasswordSyncMessage JSON: %v", err)
	}
	if payload.UPN != msg.UPN || payload.Password != msg.Password {
		t.Fatalf("payload = %+v, want UPN and password preserved", payload)
	}
	if got.ApplicationProperties["kind"] != "password-sync" {
		t.Fatalf("kind property = %v", got.ApplicationProperties["kind"])
	}
	assertNoPasswordMetadata(t, got.ApplicationProperties)
}

func TestQueuePropagatesSendError(t *testing.T) {
	t.Parallel()

	sender := &captureSender{err: errors.New("service bus unavailable")}
	queue := New(sender, 300*time.Second)

	err := queue.EnqueuePasswordSync(context.Background(), migration.PasswordSyncMessage{
		CN:          "311551001",
		UPN:         "311551001@nycu.edu.tw",
		Password:    "secret",
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
		EnqueuedAt:  time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC),
	})

	if err == nil {
		t.Fatal("EnqueuePasswordSync returned nil error")
	}
	if !strings.Contains(err.Error(), "send password sync message") {
		t.Fatalf("error = %v, want wrapped send context", err)
	}
}

func TestQueueCloseClosesSenderAndClient(t *testing.T) {
	t.Parallel()

	sender := &captureSender{}
	client := &captureCloser{}
	queue := NewWithClient(sender, client, 300*time.Second)

	if err := queue.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !sender.closed {
		t.Fatal("sender was not closed")
	}
	if !client.closed {
		t.Fatal("client was not closed")
	}
}

type captureSender struct {
	messages []*azservicebus.Message
	err      error
	closed   bool
}

func (s *captureSender) SendMessage(_ context.Context, msg *azservicebus.Message, _ *azservicebus.SendMessageOptions) error {
	if s.err != nil {
		return s.err
	}
	s.messages = append(s.messages, msg)
	return nil
}

func (s *captureSender) Close(context.Context) error {
	s.closed = true
	return nil
}

type captureCloser struct {
	closed bool
}

func (c *captureCloser) Close(context.Context) error {
	c.closed = true
	return nil
}

func assertNoPasswordMetadata(t *testing.T, props map[string]any) {
	t.Helper()
	for key, value := range props {
		text := strings.ToLower(fmt.Sprintf("%s=%v", key, value))
		if strings.Contains(text, "password") || strings.Contains(text, "cleartext") || strings.Contains(text, "secret") {
			t.Fatalf("metadata leaked password-like value: %s", text)
		}
	}
}
```

- [ ] **Step 3: Run queue tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/servicebusqueue
```

Expected: FAIL because `New`, `NewWithClient`, `Queue`, and `EnqueuePasswordSync` are not implemented.

- [ ] **Step 4: Implement queue adapter**

Create `internal/servicebusqueue/queue.go`:

```go
package servicebusqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/nycu/password-hook-service/internal/migration"
)

type sender interface {
	SendMessage(context.Context, *azservicebus.Message, *azservicebus.SendMessageOptions) error
	Close(context.Context) error
}

type closer interface {
	Close(context.Context) error
}

type Queue struct {
	sender sender
	client closer
	ttl    time.Duration
}

func New(sender sender, ttl time.Duration) *Queue {
	return NewWithClient(sender, nil, ttl)
}

func NewWithClient(sender sender, client closer, ttl time.Duration) *Queue {
	if ttl <= 0 {
		ttl = 300 * time.Second
	}
	return &Queue{
		sender: sender,
		client: client,
		ttl:    ttl,
	}
}

func NewFromConnectionString(connectionString string, queueName string, ttl time.Duration) (*Queue, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}
	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, fmt.Errorf("create service bus sender: %w", err)
	}
	return NewWithClient(sender, client, ttl), nil
}

func (q *Queue) EnqueuePasswordSync(ctx context.Context, msg migration.PasswordSyncMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal password sync message: %w", err)
	}

	contentType := "application/json"
	subject := "password-sync"
	messageID := fmt.Sprintf("%s:%s", msg.UPN, msg.EnqueuedAt.UTC().Format(time.RFC3339Nano))
	ttl := q.ttl

	serviceBusMessage := &azservicebus.Message{
		Body:        body,
		ContentType: &contentType,
		Subject:     &subject,
		MessageID:   &messageID,
		TimeToLive:  &ttl,
		ApplicationProperties: map[string]any{
			"kind": "password-sync",
			"cn":   msg.CN,
			"upn":  msg.UPN,
		},
	}

	if err := q.sender.SendMessage(ctx, serviceBusMessage, nil); err != nil {
		return fmt.Errorf("send password sync message: %w", err)
	}
	return nil
}

func (q *Queue) Close(ctx context.Context) error {
	var closeErr error
	if q.sender != nil {
		if err := q.sender.Close(ctx); err != nil {
			closeErr = fmt.Errorf("close service bus sender: %w", err)
		}
	}
	if q.client != nil {
		if err := q.client.Close(ctx); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close service bus client: %w", err)
		}
	}
	return closeErr
}
```

- [ ] **Step 5: Run focused queue tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/servicebusqueue
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go
git commit -m "feat: add service bus password sync queue"
```

---

### Task 4: Wire Service Bus Producer into App

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing app-level tests with queue injection**

Replace `internal/app/app_test.go` with:

```go
package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/migration"
)

func TestAppHookRouteEnqueuesInternalIdentity(t *testing.T) {
	t.Parallel()

	logs, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Request-ID", "trace-123")
	signRequest(req, cfg.HMACSecret, body)
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	if queue.messages[0].UPN != "311551001@nycu.edu.tw" {
		t.Fatalf("queued UPN = %q", queue.messages[0].UPN)
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"traceId":"trace-123"`)) {
		t.Fatalf("logs missing traceId: %s", logs.String())
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("logs leaked password: %s", logs.String())
	}
}

func TestAppHookRouteSkipsExternalEmailWithoutEnqueue(t *testing.T) {
	t.Parallel()

	_, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 0 {
		t.Fatalf("queued %d messages, want 0", len(queue.messages))
	}
}

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

func completeAppConfig() config.Config {
	return config.Config{
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
}

func captureDefaultLogger() (*bytes.Buffer, func()) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	return &logs, func() {
		slog.SetDefault(previous)
	}
}

func signRequest(req *http.Request, secret string, body []byte) {
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)

	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
}
```

- [ ] **Step 2: Run app tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/app
```

Expected: FAIL because `NewWithQueue` does not exist and `app.New` still uses `discardQueue`.

- [ ] **Step 3: Implement app queue wiring**

In `internal/app/app.go`:

```go
import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nycu/password-hook-service/internal/buildinfo"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/handler"
	"github.com/nycu/password-hook-service/internal/httpserver"
	"github.com/nycu/password-hook-service/internal/middleware"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/servicebusqueue"
)

type App struct {
	server *httpserver.Server
	closer interface {
		Close(context.Context) error
	}
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	queue, err := servicebusqueue.NewFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return nil, err
	}
	return newWithQueue(cfg, queue, queue)
}

func NewWithQueue(cfg config.Config, queue migration.Queue) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if queue == nil {
		return nil, errors.New("migration queue is required")
	}
	return newWithQueue(cfg, queue, nil)
}

func newWithQueue(cfg config.Config, queue migration.Queue, closer interface{ Close(context.Context) error }) (*App, error) {
	service := migration.NewService(cfg.EntraPrimaryDomain, queue)
	hook := handler.NewHook(service, cfg.ProblemBaseURL)
	hmacMiddleware, err := middleware.NewHMACWithProblemBase(cfg.HMACSecret, middleware.NewMemoryNonceStore(cfg.NonceTTL), cfg.HMACClockSkew, cfg.ProblemBaseURL)
	if err != nil {
		return nil, err
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

	return &App{server: server, closer: closer}, nil
}

func (a *App) Run(ctx context.Context) error {
	err := a.server.Run(ctx)
	if a.closer == nil {
		return err
	}
	closeErr := a.closer.Close(context.Background())
	if err != nil {
		return err
	}
	return closeErr
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.server.ServeHTTP(w, r)
}
```

Remove the old `discardQueue` type.

- [ ] **Step 4: Run focused app tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire hook producer to service bus queue"
```

---

### Task 5: Update Runtime Docs and Roadmap

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md`

- [ ] **Step 1: Update README current scope**

In `README.md`, change the current scope text so it says Slice 2 now includes producer-side Service Bus enqueueing:

```markdown
## Current Scope

This service currently implements the HTTP foundation and producer-side Azure Service Bus enqueueing:

- Go module and package structure
- `GET /healthz`
- `GET /version`
- `POST /api/v1/hook/password`
- HMAC request-signing middleware
- RFC 9457 problem responses
- password/secret masking helper
- request ID propagation
- source allowlist and anomalous request rate limiting
- identity classification and UPN building primitives
- Azure Service Bus producer for eligible internal student/employee IDs
- 300 second Service Bus message TTL for password sync jobs

Microsoft Graph, Key Vault, worker consumption, retry/DLQ policy, Terraform resources, and CI/CD security gates are implemented in later slices.
```

- [ ] **Step 2: Update README configuration**

Add Service Bus variables to the configuration table:

```markdown
| `SERVICEBUS_CONNECTION_STRING` | empty | Azure Service Bus connection string used by the producer until Slice 3 moves secret loading to Key Vault |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
```

Add these exports to the local run section:

```bash
export SERVICEBUS_CONNECTION_STRING="Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA=="
export SERVICEBUS_QUEUE_NAME="password-sync"
```

- [ ] **Step 3: Update roadmap active plan**

Update `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md`:

```markdown
Current active slice:

- Slice 2: `docs/superpowers/plans/2026-06-25-producer-servicebus.md`
```

Update completion tracking:

```markdown
| 2. Producer to Service Bus | In progress | `2026-06-25-producer-servicebus.md` | Producer-side Service Bus plan created |
```

Do not mark Slice 2 done until full verification and review are complete.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md
git commit -m "docs: document service bus producer configuration"
```

---

### Task 6: Full Verification and Slice Review Prep

**Files:**
- Review all changed files.

- [ ] **Step 1: Run full verification**

Run:

```bash
make verify
```

Expected: PASS for `gofmt -w .`, `go test ./...`, and `go vet ./...`.

- [ ] **Step 2: Verify Slice 2 done criteria**

Check each item manually:

```text
Internal student/employee IDs enqueue to Azure Service Bus:
- App production path builds servicebusqueue from config.
- migration.Service still enqueues only eligible internal IDs.
- servicebusqueue sends JSON PasswordSyncMessage.

TTL 300s:
- Config default is 300 seconds.
- servicebusqueue sets azservicebus.Message.TimeToLive to cfg.PasswordMessageTTL.
- Tests assert TimeToLive is 300 seconds.

External emails skip without enqueue:
- Existing handler/migration tests remain passing.
- App-level test verifies external email request returns 202 and fake queue receives zero messages.

Password is not logged:
- App-level test verifies access logs do not contain request password.
- servicebusqueue metadata test verifies ApplicationProperties do not contain password-like data.
```

- [ ] **Step 3: Inspect dependency footprint**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go list -m all
```

Expected: Azure SDK modules are present because Slice 2 introduces Service Bus. No Graph, Key Vault, Terraform provider, or worker-specific dependency is introduced by this slice.

- [ ] **Step 4: Commit verification fixes if needed**

If verification changes files or exposes issues, fix them with TDD and commit:

```bash
git add Makefile README.md go.mod go.sum internal/config/config.go internal/config/config_test.go internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go internal/app/app.go internal/app/app_test.go docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md
git commit -m "test: verify service bus producer slice"
```

If no files changed, do not create an empty commit.

- [ ] **Step 5: Request code review**

Use `superpowers:requesting-code-review` with:

```text
Description: Slice 2 producer-side Service Bus enqueueing for password sync jobs.
Requirements: docs/superpowers/plans/2026-06-25-producer-servicebus.md and Slice 2 roadmap criteria.
Base SHA: ef9d686
Head SHA: current HEAD after Slice 2 implementation.
```

Expected review focus:
- Service Bus message TTL is exactly 300 seconds.
- External email identities still skip without enqueue.
- Password appears only in encrypted Service Bus message body and not in logs or message metadata.
- App production path no longer uses `discardQueue`.
- No worker, Graph, Key Vault, retry/DLQ, or infrastructure behavior leaked into Slice 2.

---

## Self-Review

Spec coverage:
- Slice 2 roadmap criteria are covered by Tasks 2-6.
- Design document Service Bus producer behavior is covered by `internal/servicebusqueue` and `app.New` wiring.
- Worker, Graph, Key Vault, retry/DLQ, password zeroing, metrics, and infrastructure are intentionally deferred to later slices.

Placeholder scan:
- This plan contains no unfinished marker text and no open-ended implementation instructions.

Type consistency:
- `migration.Queue.EnqueuePasswordSync(context.Context, migration.PasswordSyncMessage) error` remains the core boundary.
- `servicebusqueue.Queue` implements `migration.Queue`.
- `app.NewWithQueue` is the test seam; production `app.New` constructs `servicebusqueue.Queue`.
- `Config.PasswordMessageTTL` is a `time.Duration` and maps directly to `azservicebus.Message.TimeToLive`.
