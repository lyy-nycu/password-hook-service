# Slice 6 Microsoft Graph Client Implementation Plan

> **Plan Status:** Completed
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the real Microsoft Graph password sync path so queued internal accounts create missing Entra users, patch existing users, and classify Graph failures for the existing worker retry/DLQ policy.

**Architecture:** Keep Microsoft Graph HTTP behavior isolated in `internal/graphclient`, with no worker or Service Bus dependency. Add a small `internal/graphprocessor` adapter that translates `worker.PasswordSyncCommand` into Graph client calls and maps Graph permanent failures to `worker.PermanentError`. Wire the production app to run the HTTP server and Service Bus worker together, sharing the existing password decryptor, receiver, safe DLQ sink, and Graph processor.

**Tech Stack:** Go 1.26, standard-library `net/http`, Azure `azidentity.ClientSecretCredential` for OAuth2 client credentials, existing Azure Service Bus adapter, existing worker retry/DLQ loop, `httptest` based Graph tests.

---

## Scope

This plan implements Slice 6 only.

In scope:

- Graph app-only OAuth token acquisition using the existing `GRAPH_TENANT_ID`, `GRAPH_CLIENT_ID`, and `GRAPH_CLIENT_SECRET` configuration.
- Graph user lookup by UPN.
- `PATCH /v1.0/users/{upn}` for existing users.
- `POST /v1.0/users` for missing users.
- Graph failure classification for `400`, `403`, `429`, `503`, and network errors.
- Worker processor adapter that preserves the current borrowed `[]byte` password lifetime.
- Production wiring so the worker consumes encrypted Service Bus messages and calls Graph.

Out of scope:

- Graph throughput token buckets and metrics; Slice 8 owns observability.
- Terraform/App Registration changes; Slice 10 owns infrastructure.
- Any external-email migration behavior; Phase 1 still skips those before enqueue.
- Broad password-memory hardening beyond avoiding avoidable string retention and zeroing request bodies after Graph attempts.

## Current Context

- `internal/graphclient/client.go` is only a placeholder interface.
- `internal/worker/worker.go` already decrypts per attempt, passes `worker.PasswordSyncCommand.Password` as borrowed `[]byte`, retries transient processor errors, and treats `*worker.PermanentError` as terminal safe-DLQ failures.
- `internal/servicebusqueue/queue.go` already provides a production `worker.Receiver`.
- `internal/servicebusqueue/deadletter.go` already provides a production safe DLQ sink.
- `internal/app/app.go` currently constructs only the hook HTTP server and producer queue; it does not start the worker.

## File Structure

- Modify: `internal/graphclient/client.go` - replace the placeholder with the Graph HTTP client, request builders, token credential interface, and error classification.
- Create: `internal/graphclient/client_test.go` - cover existing-user patch, missing-user create, request body shape, auth header, status classification, network classification, and request body zeroing.
- Create: `internal/graphprocessor/processor.go` - implement `worker.Processor` using `graphclient.Client`.
- Create: `internal/graphprocessor/processor_test.go` - cover command mapping and permanent/transient error translation.
- Modify: `internal/config/config.go` - require Graph credentials for full production validation.
- Modify: `internal/config/config_test.go` - replace the current "Graph credentials may be missing" expectation with required-credential tests.
- Modify: `internal/app/app.go` - build Graph credential/client/processor, Service Bus receiver, safe DLQ sink, worker, and run server plus worker together.
- Modify: `internal/app/app_test.go` - keep `NewWithQueue` HTTP-only tests lightweight and add focused tests for full-mode Graph credential validation and worker construction.
- Modify: `README.md` - document Graph credential requirements and worker behavior.
- Modify: `docs/superpowers/plans/roadmap.md` - mark Slice 6 active during implementation, then completed after implementation.

---

## Task 1: Replace Placeholder With Graph Client Contract Tests

**Files:**
- Modify: `internal/graphclient/client.go`
- Create: `internal/graphclient/client_test.go`

- [ ] **Step 1: Write failing tests for update, create, and authorization**

Create `internal/graphclient/client_test.go` with tests that start an `httptest.Server`, inject a fake token credential, and assert these flows:

```go
func TestUpsertUserPasswordPatchesExistingUser(t *testing.T)
func TestUpsertUserPasswordCreatesMissingUser(t *testing.T)
func TestUpsertUserPasswordSendsBearerToken(t *testing.T)
```

Test expectations:

- Existing user flow receives `GET /v1.0/users/311551001%40nycu.edu.tw`, returns `200`, then receives `PATCH /v1.0/users/311551001%40nycu.edu.tw`.
- Missing user flow receives `GET`, returns `404`, then receives `POST /v1.0/users`.
- Every Graph request has `Authorization: Bearer test-token`.
- Patch body contains `passwordProfile.password`, `passwordProfile.forceChangePasswordNextSignIn: false`, and no `displayName` mutation.
- Create body contains `accountEnabled: true`, `displayName`, `mailNickname`, `userPrincipalName`, optional `mail`, optional `otherMails`, and `passwordProfile`.

- [ ] **Step 2: Write failing tests for error classification**

Add table-driven tests:

```go
func TestUpsertUserPasswordClassifiesGraphStatuses(t *testing.T)
```

Required classification:

| Status / failure | Expected error |
|---|---|
| `400` | `*graphclient.PermanentError` |
| `403` | `*graphclient.PermanentError` |
| `429` | `*graphclient.TransientError` |
| `503` | `*graphclient.TransientError` |
| network error | `*graphclient.TransientError` |

- [ ] **Step 3: Write a failing request-body zeroing test**

Add:

```go
func TestUpsertUserPasswordClearsRequestBodyBufferAfterAttempt(t *testing.T)
```

Use a test hook on the client, for example `AfterRequestBodyBuilt func([]byte)`, to capture the actual mutable request body buffer. After `UpsertUserPassword` returns, assert the captured buffer no longer contains `cleartext-password`.

- [ ] **Step 4: Run focused tests and confirm failure**

Run:

```bash
go test ./internal/graphclient -v
```

Expected: FAIL because the placeholder `internal/graphclient.Client` interface does not implement the HTTP behavior, constructors, or error types.

## Task 2: Implement Microsoft Graph HTTP Client

**Files:**
- Modify: `internal/graphclient/client.go`

- [ ] **Step 1: Replace the placeholder interface with concrete types**

Implement these public types in `internal/graphclient/client.go`:

```go
package graphclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

const defaultBaseURL = "https://graph.microsoft.com"

type User struct {
	UPN         string
	DisplayName string
	Mail        string
}

type Client interface {
	UpsertUserPassword(context.Context, User, []byte) error
}

type TokenCredential interface {
	GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)
}

type Options struct {
	BaseURL               string
	HTTPClient            *http.Client
	Scope                 string
	AfterRequestBodyBuilt func([]byte)
}

type HTTPClient struct {
	baseURL               *url.URL
	httpClient            *http.Client
	credential            TokenCredential
	scope                 string
	afterRequestBodyBuilt func([]byte)
}

type PermanentError struct {
	StatusCode int
	Operation  string
	Err        error
}

type TransientError struct {
	StatusCode int
	Operation  string
	Err        error
}
```

- [ ] **Step 2: Add constructor validation**

Implement:

```go
func NewHTTPClient(credential TokenCredential, options Options) (*HTTPClient, error)
```

Validation rules:

- credential is required.
- base URL defaults to `https://graph.microsoft.com`.
- malformed base URL returns `graph base URL is invalid: ...`.
- HTTP client defaults to `http.DefaultClient`.
- scope defaults to `https://graph.microsoft.com/.default`.

- [ ] **Step 3: Implement upsert flow**

Implement:

```go
func (c *HTTPClient) UpsertUserPassword(ctx context.Context, user User, password []byte) error
```

Rules:

- Trim and require `user.UPN`.
- Require non-empty `password`.
- `GET /v1.0/users/{url.PathEscape(upn)}` first.
- On `200`, call patch.
- On `404`, call create.
- On other status, classify the GET response.
- Do not log or return the password.

- [ ] **Step 4: Implement request builders without retaining password strings**

Build JSON request bodies into mutable `[]byte` buffers. Use a helper that JSON-escapes password bytes directly instead of converting password to a long-lived field:

```go
func appendJSONString(dst []byte, value []byte) []byte
func appendJSONStringFromString(dst []byte, value string) []byte
func mailNickname(upn string) string
```

Call `passwordcrypto.ZeroBytes(body)` with `defer` immediately after each POST/PATCH request is created so the buffer is cleared after the Graph attempt completes.

- [ ] **Step 5: Implement token acquisition and HTTP execution**

Before each Graph request:

- Get a token with `TokenRequestOptions{Scopes: []string{scope}}`.
- Set `Authorization: Bearer <token>`.
- Set `Content-Type: application/json` for POST/PATCH.
- Set `Accept: application/json`.
- Close every response body.

- [ ] **Step 6: Implement error classification**

Classification:

```go
func classifyGraphResponse(operation string, status int) error {
	switch status {
	case http.StatusBadRequest, http.StatusForbidden:
		return &PermanentError{StatusCode: status, Operation: operation}
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return &TransientError{StatusCode: status, Operation: operation}
	default:
		return &TransientError{StatusCode: status, Operation: operation}
	}
}
```

Network and token acquisition errors should return `*TransientError` so the existing worker retry loop can retry temporary outages.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./internal/graphclient -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/graphclient/client.go internal/graphclient/client_test.go
git commit -m "feat: add microsoft graph password client"
```

## Task 3: Add Worker Processor Adapter

**Files:**
- Create: `internal/graphprocessor/processor.go`
- Create: `internal/graphprocessor/processor_test.go`

- [ ] **Step 1: Write failing processor tests**

Create `internal/graphprocessor/processor_test.go` with:

```go
func TestProcessorMapsWorkerCommandToGraphUser(t *testing.T)
func TestProcessorMapsPermanentGraphErrorToWorkerPermanentError(t *testing.T)
func TestProcessorLeavesTransientGraphErrorRetryable(t *testing.T)
```

Assertions:

- `CN` is not sent to Graph.
- `UPN`, `DisplayName`, `Mail`, and borrowed `Password []byte` are passed to `graphclient.Client`.
- `*graphclient.PermanentError` becomes `*worker.PermanentError` with `worker.PermanentReasonProcessorError`.
- `*graphclient.TransientError` is returned unchanged so worker retries it.

- [ ] **Step 2: Implement processor**

Create `internal/graphprocessor/processor.go`:

```go
package graphprocessor

import (
	"context"
	"errors"

	"github.com/nycu/password-hook-service/internal/graphclient"
	"github.com/nycu/password-hook-service/internal/worker"
)

type Processor struct {
	client graphclient.Client
}

func New(client graphclient.Client) (*Processor, error) {
	if client == nil {
		return nil, errors.New("graph client is required")
	}
	return &Processor{client: client}, nil
}

func (p *Processor) ProcessPasswordSync(ctx context.Context, msg worker.PasswordSyncCommand) error {
	err := p.client.UpsertUserPassword(ctx, graphclient.User{
		UPN:         msg.UPN,
		DisplayName: msg.DisplayName,
		Mail:        msg.Mail,
	}, msg.Password)
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

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test ./internal/graphprocessor -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/graphprocessor/processor.go internal/graphprocessor/processor_test.go
git commit -m "feat: adapt worker messages to graph client"
```

## Task 4: Require Graph Credentials For Full Production Mode

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Replace the permissive Graph credential test**

In `internal/config/config_test.go`, replace `TestValidateAllowsMissingGraphCredentials` with:

```go
func TestValidateRequiresGraphCredentials(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "tenant", edit: func(cfg *Config) { cfg.GraphTenantID = "" }, want: "GRAPH_TENANT_ID is required"},
		{name: "client", edit: func(cfg *Config) { cfg.GraphClientID = "" }, want: "GRAPH_CLIENT_ID is required"},
		{name: "secret", edit: func(cfg *Config) { cfg.GraphClientSecret = "" }, want: "GRAPH_CLIENT_SECRET is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := completeConfig()
			tt.edit(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate returned nil error")
			}
			if err.Error() != tt.want {
				t.Fatalf("Validate error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Add Graph credential checks to `Config.Validate`**

After password encryption validation in `internal/config/config.go`, require:

```go
case strings.TrimSpace(c.GraphTenantID) == "":
	return errors.New("GRAPH_TENANT_ID is required")
case strings.TrimSpace(c.GraphClientID) == "":
	return errors.New("GRAPH_CLIENT_ID is required")
case strings.TrimSpace(c.GraphClientSecret) == "":
	return errors.New("GRAPH_CLIENT_SECRET is required")
```

Keep `ValidateHTTP` free of Graph requirements so `app.NewWithQueue` tests can still construct HTTP-only hook apps.

- [ ] **Step 3: Run config tests**

Run:

```bash
go test ./internal/config -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: require graph credentials in production config"
```

## Task 5: Wire Graph Worker Into Production App

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Add app tests for full production wiring**

Add tests that use fake receiver, fake processor, fake dead-letter sink, and fake closer components through a new unexported constructor. Required tests:

```go
func TestNewRequiresGraphCredentialsInFullMode(t *testing.T)
func TestRunStartsWorkerAndHTTPServer(t *testing.T)
func TestRunClosesAllOwnedResources(t *testing.T)
```

Expected behavior:

- `New(cfg)` rejects missing Graph credentials through `cfg.Validate`.
- `Run(ctx)` starts the HTTP server and worker under the same cancellation context.
- `Run(ctx)` closes sender, receiver, and safe DLQ resources with bounded contexts.

- [ ] **Step 2: Refactor `App` to hold multiple closers and an optional worker**

Change `App` to:

```go
type App struct {
	server *httpserver.Server
	worker interface {
		Run(context.Context) error
	}
	closers []interface {
		Close(context.Context) error
	}
}
```

- [ ] **Step 3: Build production dependencies in `New`**

In `app.New(cfg)`:

- Validate full config with `cfg.Validate()`.
- Build Service Bus sender queue with `servicebusqueue.NewFromConnectionString`.
- Build Service Bus receiver with `servicebusqueue.NewReceiverFromConnectionString`.
- Build safe DLQ queue with `servicebusqueue.NewDeadLetterQueueFromConnectionString`.
- Build password codec with `passwordcrypto.NewCodecFromBase64`.
- Build Graph credential with `azidentity.NewClientSecretCredential(cfg.GraphTenantID, cfg.GraphClientID, cfg.GraphClientSecret, nil)`.
- Build Graph client with `graphclient.NewHTTPClient`.
- Build graph processor with `graphprocessor.New`.
- Build worker with `worker.New(receiver, processor, worker.Options{DeadLetterSink: dlq, PasswordDecrypter: passwordCodec})`.
- Return an app that owns queue, receiver, and DLQ closers.

- [ ] **Step 4: Keep `NewWithQueue` HTTP-only**

`NewWithQueue` should continue to call `ValidateHTTP` plus password encryption validation only. It is a test helper for hook route behavior and must not require Service Bus, Graph credentials, a receiver, or a safe DLQ sink.

- [ ] **Step 5: Run server and worker together**

Implement `Run(ctx)` so:

- If no worker exists, preserve current HTTP-only behavior.
- If a worker exists, run server and worker concurrently.
- Cancel both when either returns a non-nil error or when the input context is canceled.
- Close all owned resources after runtime exits.
- Join close errors with runtime errors.

- [ ] **Step 6: Run app tests**

Run:

```bash
go test ./internal/app -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: run graph-backed password worker"
```

## Task 6: Verify Worker Integration And Security Scans

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/roadmap.md`

- [ ] **Step 1: Update README runtime documentation**

Document:

- `GRAPH_TENANT_ID`, `GRAPH_CLIENT_ID`, and `GRAPH_CLIENT_SECRET` are required for production `app.New`.
- The worker consumes encrypted queue messages, decrypts per attempt, calls Microsoft Graph, and uses the safe DLQ on terminal failures.
- Required Graph application permission remains `User.ReadWrite.All` from the approved design.

- [ ] **Step 2: Update roadmap status**

After implementation and verification, update `docs/superpowers/plans/roadmap.md`:

- Move Slice 6 from `Active` to `Done`.
- Point the detailed plan to `completed/2026-07-02-slice-06-microsoft-graph-client.md` after moving this file from `active/` to `completed/`.
- Set "Active Detailed Plan" to the next slice after Slice 6.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
go test ./internal/graphclient ./internal/graphprocessor ./internal/worker ./internal/app ./internal/config -v
```

Expected: PASS.

- [ ] **Step 4: Run full verification**

Run:

```bash
gofmt -w internal/graphclient internal/graphprocessor internal/config internal/app
go test ./...
go vet ./...
```

Expected: all commands exit 0.

- [ ] **Step 5: Run leak-focused scans**

Run:

```bash
rg -n 'password.*slog|slog.*password|Password string|json:"password"|string\(.*Password|fmt\..*password' internal/graphclient internal/graphprocessor internal/worker internal/app
rg -n 'password|Password|passwordProfile' internal/servicebusqueue internal/worker internal/graphclient
```

Expected:

- No logging of plaintext password.
- No Service Bus body or application property stores plaintext password.
- Graph request body references are limited to the Graph HTTP client and are zeroed after attempts.

- [ ] **Step 6: Commit docs and plan status**

```bash
git add README.md docs/superpowers/plans/roadmap.md docs/superpowers/plans/completed/2026-07-02-slice-06-microsoft-graph-client.md
git add -u docs/superpowers/plans/active/2026-07-02-slice-06-microsoft-graph-client.md
git commit -m "docs: mark graph client slice complete"
```

---

## Review Checklist

- Slice 6 roadmap done criteria are covered: existing users patch, missing users create, and `400/403/429/503/network` classification.
- Graph behavior is isolated behind `internal/graphclient` and tested with HTTP test servers.
- Worker retry/DLQ behavior stays in `internal/worker`; Graph only returns classified errors.
- The borrowed `[]byte` password handoff from the worker is preserved.
- Production app wiring starts the worker only in full `app.New`, while `NewWithQueue` stays lightweight for HTTP route tests.
- No task introduces infrastructure, metrics, CI scanners, or external identity migration work.
