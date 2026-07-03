# Slice 10A Service Bus Managed Identity Draft Implementation Plan

> **Status:** Draft. This plan is a future-slice planning artifact only. Do not execute it until Slice 7 is merged, Slice 8 and Slice 9 are refreshed or implemented as needed, this draft is refreshed against `main`, and the owner decides whether to insert this as a real slice before Infrastructure.
>
> **For agentic workers:** REQUIRED SUB-SKILL WHEN PROMOTED: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the password hook service use Azure Managed Identity directly for Service Bus send/receive/safe-DLQ operations so production no longer needs a `SERVICEBUS_CONNECTION_STRING` secret.

**Architecture:** Add a Service Bus authentication mode to config and app wiring: `connection_string` remains the local/backward-compatible path, while `managed_identity` uses Azure Identity plus the Service Bus fully qualified namespace. Keep queue, receiver, and safe-DLQ business behavior unchanged by adding new constructors around the same `azservicebus.Client` sender/receiver primitives.

**Tech Stack:** Go, Azure SDK for Go `azidentity`, `azcore.TokenCredential`, `azservicebus.NewClient(fullyQualifiedNamespace, credential, nil)`, existing `internal/config`, `internal/secretloader`, `internal/servicebusqueue`, `internal/app`, unit tests with fakes.

---

## Draft Constraints

- Draft only. Do not execute this plan until it is refreshed and promoted to `active/`.
- Do not update `docs/superpowers/plans/README.md`, `docs/superpowers/plans/roadmap.md`, or any active plan pointer while this remains a draft.
- Do not remove connection-string mode in this slice. Local development and rollback should continue to work with `SERVICEBUS_AUTH_MODE=connection_string`.
- Do not change message schema, password encryption, worker retry behavior, safe DLQ payload shape, or Graph behavior.
- Do not add Terraform, Azure RBAC, or Container Apps resources in this slice. Infrastructure should consume the new app config in the later infrastructure slice.
- Do not store Service Bus connection strings in Terraform state or require a Service Bus connection string secret when `SERVICEBUS_AUTH_MODE=managed_identity`.
- Recheck the Azure SDK API at promotion time. The current documented constructor supports Azure Identity credentials via `azservicebus.NewClient("<namespace>.servicebus.windows.net", credential, nil)`.

## Current Context

- `internal/servicebusqueue/queue.go` and `deadletter.go` only expose production constructors that use `azservicebus.NewClientFromConnectionString`.
- `internal/app/app.go` wires producer queue, worker receiver, and safe DLQ using `cfg.ServiceBusConnectionString`.
- `internal/config/config.go` requires `SERVICEBUS_CONNECTION_STRING` in full `Validate()`.
- `internal/secretloader/loader.go` always loads the Key Vault secret named by `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME`.
- `README.md` documents `SERVICEBUS_CONNECTION_STRING` as required.
- Slice 10 Infrastructure draft currently avoids Terraform-managed Service Bus SAS rules because those generated keys enter Terraform state. Managed Identity support is the cleaner long-term path.

## Managed Identity Story

- Production should set `SERVICEBUS_AUTH_MODE=managed_identity`.
- Production should set `SERVICEBUS_NAMESPACE_FQDN=<namespace>.servicebus.windows.net`.
- The Container App's managed identity receives Azure Service Bus RBAC assignments in the infrastructure slice.
- The application creates one `DefaultAzureCredential`, then builds Service Bus producer, receiver, and safe-DLQ sender clients with the namespace FQDN.
- Key Vault still stores HMAC secret, Graph client secret, and password payload encryption key.
- Key Vault does not need to store `servicebus-conn-str` in managed identity mode.
- Connection-string mode remains valid for local development, tests, and emergency rollback.

## File Structure

- Modify `internal/config/config.go`: add Service Bus auth mode constants, `ServiceBusAuthMode`, and `ServiceBusNamespaceFQDN`; validate connection-string vs managed-identity requirements.
- Modify `internal/config/config_test.go`: cover loading defaults, managed identity validation, connection-string validation, and invalid auth mode.
- Modify `internal/secretloader/loader.go`: skip Service Bus connection string loading in managed identity mode and validate Key Vault secret names accordingly.
- Modify `internal/secretloader/loader_test.go`: assert managed identity mode does not read `servicebus-conn-str`.
- Modify `internal/servicebusqueue/queue.go`: add namespace/credential constructors for producer and receiver.
- Modify `internal/servicebusqueue/deadletter.go`: add namespace/credential constructor for safe DLQ.
- Modify `internal/servicebusqueue/queue_test.go` and `deadletter_test.go`: cover constructor validation and error wrapping without network calls.
- Modify `internal/app/app.go`: choose connection-string or managed-identity Service Bus wiring from config.
- Modify `internal/app/app_test.go`: inject a fake Service Bus runtime builder and assert managed identity mode does not require connection string.
- Modify `README.md`: document `SERVICEBUS_AUTH_MODE`, `SERVICEBUS_NAMESPACE_FQDN`, local fallback, and production RBAC expectation.
- Optionally refresh `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`: after this slice is accepted, remove Service Bus connection string injection from the preferred infrastructure story and replace it with RBAC.

---

### Task 1: Service Bus Auth Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestLoadServiceBusAuthDefaultsToConnectionString(t *testing.T) {
	t.Setenv("SERVICEBUS_AUTH_MODE", "")
	t.Setenv("SERVICEBUS_NAMESPACE_FQDN", " ")

	cfg := Load()

	if cfg.ServiceBusAuthMode != ServiceBusAuthConnectionString {
		t.Fatalf("ServiceBusAuthMode = %q, want %q", cfg.ServiceBusAuthMode, ServiceBusAuthConnectionString)
	}
	if cfg.ServiceBusNamespaceFQDN != "" {
		t.Fatalf("ServiceBusNamespaceFQDN = %q, want empty", cfg.ServiceBusNamespaceFQDN)
	}
}

func TestLoadServiceBusManagedIdentityConfig(t *testing.T) {
	t.Setenv("SERVICEBUS_AUTH_MODE", " managed_identity ")
	t.Setenv("SERVICEBUS_NAMESPACE_FQDN", " nycu-password-hook.servicebus.windows.net ")

	cfg := Load()

	if cfg.ServiceBusAuthMode != ServiceBusAuthManagedIdentity {
		t.Fatalf("ServiceBusAuthMode = %q, want managed_identity", cfg.ServiceBusAuthMode)
	}
	if cfg.ServiceBusNamespaceFQDN != "nycu-password-hook.servicebus.windows.net" {
		t.Fatalf("ServiceBusNamespaceFQDN = %q", cfg.ServiceBusNamespaceFQDN)
	}
}

func TestValidateManagedIdentityRequiresNamespaceNotConnectionString(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusAuthMode = ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "nycu-password-hook.servicebus.windows.net"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateManagedIdentityRequiresNamespaceFQDN(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusAuthMode = ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "SERVICEBUS_NAMESPACE_FQDN is required when SERVICEBUS_AUTH_MODE=managed_identity" {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestValidateRejectsInvalidServiceBusAuthMode(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusAuthMode = "sas"

	err := cfg.Validate()
	if err == nil || err.Error() != "SERVICEBUS_AUTH_MODE must be connection_string or managed_identity" {
		t.Fatalf("Validate error = %v", err)
	}
}
```

- [ ] **Step 2: Run config tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/config`

Expected: FAIL because `ServiceBusAuthMode`, `ServiceBusNamespaceFQDN`, and auth-mode validation do not exist.

- [ ] **Step 3: Add auth mode constants and fields**

Edit `internal/config/config.go`:

```go
const (
	SecretsSourceEnv      = "env"
	SecretsSourceKeyVault = "keyvault"

	ServiceBusAuthConnectionString = "connection_string"
	ServiceBusAuthManagedIdentity  = "managed_identity"
)
```

Add these new fields near the existing Service Bus fields in `Config`:

```go
ServiceBusAuthMode      string
ServiceBusNamespaceFQDN string
```

Change `Load()`:

```go
ServiceBusAuthMode:             env("SERVICEBUS_AUTH_MODE", ServiceBusAuthConnectionString),
ServiceBusNamespaceFQDN:        strings.TrimSpace(os.Getenv("SERVICEBUS_NAMESPACE_FQDN")),
ServiceBusConnectionString:     strings.TrimSpace(os.Getenv("SERVICEBUS_CONNECTION_STRING")),
ServiceBusQueueName:            env("SERVICEBUS_QUEUE_NAME", "password-sync"),
ServiceBusDeadLetterQueueName:  env("SERVICEBUS_DEADLETTER_QUEUE_NAME", "password-sync-dlq"),
```

- [ ] **Step 4: Split Service Bus validation by auth mode**

Replace the Service Bus portion of `Validate()` with:

```go
	if err := c.validateServiceBus(); err != nil {
		return err
	}
```

Add:

```go
func (c Config) validateServiceBus() error {
	switch c.ServiceBusAuthMode {
	case "", ServiceBusAuthConnectionString:
		if strings.TrimSpace(c.ServiceBusConnectionString) == "" {
			return errors.New("SERVICEBUS_CONNECTION_STRING is required")
		}
	case ServiceBusAuthManagedIdentity:
		if strings.TrimSpace(c.ServiceBusNamespaceFQDN) == "" {
			return errors.New("SERVICEBUS_NAMESPACE_FQDN is required when SERVICEBUS_AUTH_MODE=managed_identity")
		}
		if strings.Contains(c.ServiceBusNamespaceFQDN, "://") || !strings.HasSuffix(c.ServiceBusNamespaceFQDN, ".servicebus.windows.net") {
			return errors.New("SERVICEBUS_NAMESPACE_FQDN must be a Service Bus namespace host name")
		}
	default:
		return errors.New("SERVICEBUS_AUTH_MODE must be connection_string or managed_identity")
	}
	switch {
	case strings.TrimSpace(c.ServiceBusQueueName) == "":
		return errors.New("SERVICEBUS_QUEUE_NAME is required")
	case strings.TrimSpace(c.ServiceBusDeadLetterQueueName) == "":
		return errors.New("SERVICEBUS_DEADLETTER_QUEUE_NAME is required")
	case c.PasswordMessageTTL <= 0:
		return errors.New("PasswordMessageTTL must be positive")
	default:
		return nil
	}
}
```

- [ ] **Step 5: Update config helper**

In `completeConfig()` in `internal/config/config_test.go`, add:

```go
ServiceBusAuthMode:          ServiceBusAuthConnectionString,
ServiceBusNamespaceFQDN:     "",
```

- [ ] **Step 6: Run config tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/config`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add service bus auth mode config"
```

### Task 2: Key Vault Loading Skips Service Bus Secret In Managed Identity Mode

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/secretloader/loader.go`
- Modify: `internal/secretloader/loader_test.go`

- [ ] **Step 1: Write failing secret-loading tests**

Add to `internal/secretloader/loader_test.go`:

```go
func TestResolveKeyVaultManagedIdentitySkipsServiceBusConnectionSecret(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = config.SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.ServiceBusAuthMode = config.ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "nycu-password-hook.servicebus.windows.net"
	cfg.HMACSecret = ""
	cfg.GraphClientSecret = ""
	cfg.PasswordEncryptionKeyB64 = ""
	getter := &fakeGetter{values: map[string]string{
		"hook-hmac-secret":                "kv-hmac",
		"graph-client-secret":             "kv-graph-secret",
		"password-payload-encryption-key": base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	}}

	got, err := Resolve(context.Background(), cfg, getter)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if got.ServiceBusConnectionString != "" {
		t.Fatalf("ServiceBusConnectionString = %q, want empty", got.ServiceBusConnectionString)
	}
	wantCalls := []string{"hook-hmac-secret", "graph-client-secret", "password-payload-encryption-key"}
	if strings.Join(getter.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("calls = %v, want %v", getter.calls, wantCalls)
	}
}
```

Add to `internal/config/config_test.go`:

```go
func TestValidateKeyVaultManagedIdentityDoesNotRequireServiceBusSecretName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.ServiceBusAuthMode = ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "nycu-password-hook.servicebus.windows.net"
	cfg.KeyVaultSecretNames.ServiceBusConnectionString = ""

	if err := cfg.ValidateSecretLoadingInputs(); err != nil {
		t.Fatalf("ValidateSecretLoadingInputs returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/config ./internal/secretloader`

Expected: FAIL because Key Vault validation/loading still requires `servicebus-conn-str`.

- [ ] **Step 3: Make Key Vault Service Bus secret conditional**

In `ValidateSecretLoadingInputs()`, change the Service Bus secret-name check:

```go
case c.ServiceBusAuthMode != ServiceBusAuthManagedIdentity && strings.TrimSpace(c.KeyVaultSecretNames.ServiceBusConnectionString) == "":
	return errors.New("KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME is required when SECRETS_SOURCE=keyvault")
```

In `resolveKeyVault()`, replace unconditional Service Bus secret loading with:

```go
if cfg.ServiceBusAuthMode != config.ServiceBusAuthManagedIdentity {
	serviceBusConnectionString, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.ServiceBusConnectionString)
	if err != nil {
		return config.Config{}, err
	}
	cfg.ServiceBusConnectionString = serviceBusConnectionString
}
```

Keep HMAC, Graph client secret, and password encryption key loading unchanged.

- [ ] **Step 4: Update helper config**

In `completeConfig()` in `internal/secretloader/loader_test.go`, add:

```go
ServiceBusAuthMode:      config.ServiceBusAuthConnectionString,
ServiceBusNamespaceFQDN: "",
```

- [ ] **Step 5: Run tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/config ./internal/secretloader`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/secretloader/loader.go internal/secretloader/loader_test.go
git commit -m "feat: skip service bus secret in managed identity mode"
```

### Task 3: Service Bus Namespace Constructors

**Files:**
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/deadletter.go`
- Modify: `internal/servicebusqueue/queue_test.go`
- Modify: `internal/servicebusqueue/deadletter_test.go`

- [ ] **Step 1: Write failing constructor tests**

Add to `internal/servicebusqueue/queue_test.go`:

```go
func TestNewFromNamespaceRejectsEmptyNamespace(t *testing.T) {
	t.Parallel()

	queue, err := NewFromNamespace("", fakeTokenCredential{}, "password-sync", 300*time.Second)
	if err == nil || err.Error() != "service bus namespace FQDN is required" {
		t.Fatalf("NewFromNamespace error = %v, want namespace error", err)
	}
	if queue != nil {
		t.Fatalf("NewFromNamespace queue = %#v, want nil", queue)
	}
}

func TestNewFromNamespaceRejectsNilCredential(t *testing.T) {
	t.Parallel()

	queue, err := NewFromNamespace("example.servicebus.windows.net", nil, "password-sync", 300*time.Second)
	if err == nil || err.Error() != "service bus token credential is required" {
		t.Fatalf("NewFromNamespace error = %v, want credential error", err)
	}
	if queue != nil {
		t.Fatalf("NewFromNamespace queue = %#v, want nil", queue)
	}
}

func TestNewReceiverFromNamespaceRejectsEmptyQueue(t *testing.T) {
	t.Parallel()

	receiver, err := NewReceiverFromNamespace("example.servicebus.windows.net", fakeTokenCredential{}, "")
	if err == nil || err.Error() != "service bus queue name is required" {
		t.Fatalf("NewReceiverFromNamespace error = %v, want queue name error", err)
	}
	if receiver != nil {
		t.Fatalf("NewReceiverFromNamespace receiver = %#v, want nil", receiver)
	}
}
```

Add this fake to the test file:

```go
type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}
```

Add imports:

```go
"github.com/Azure/azure-sdk-for-go/sdk/azcore"
"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
```

Add to `internal/servicebusqueue/deadletter_test.go`:

```go
func TestNewDeadLetterQueueFromNamespaceRejectsEmptyNamespace(t *testing.T) {
	t.Parallel()

	queue, err := NewDeadLetterQueueFromNamespace("", fakeTokenCredential{}, "password-sync-dlq")
	if err == nil || err.Error() != "service bus namespace FQDN is required" {
		t.Fatalf("NewDeadLetterQueueFromNamespace error = %v, want namespace error", err)
	}
	if queue != nil {
		t.Fatalf("NewDeadLetterQueueFromNamespace queue = %#v, want nil", queue)
	}
}
```

- [ ] **Step 2: Run servicebusqueue tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: FAIL because namespace constructors do not exist.

- [ ] **Step 3: Add namespace client helper and constructors**

In `internal/servicebusqueue/queue.go`, add imports:

```go
"strings"

"github.com/Azure/azure-sdk-for-go/sdk/azcore"
```

Add helper:

```go
func newClientFromNamespace(namespaceFQDN string, credential azcore.TokenCredential) (*azservicebus.Client, error) {
	namespaceFQDN = strings.TrimSpace(namespaceFQDN)
	if namespaceFQDN == "" {
		return nil, errors.New("service bus namespace FQDN is required")
	}
	if credential == nil {
		return nil, errors.New("service bus token credential is required")
	}
	client, err := azservicebus.NewClient(namespaceFQDN, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create service bus client: %w", err)
	}
	return client, nil
}
```

Add producer constructor:

```go
func NewFromNamespace(namespaceFQDN string, credential azcore.TokenCredential, queueName string, ttl time.Duration) (*Queue, error) {
	if strings.TrimSpace(queueName) == "" {
		return nil, errors.New("service bus queue name is required")
	}
	client, err := newClientFromNamespace(namespaceFQDN, credential)
	if err != nil {
		return nil, err
	}

	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create service bus sender: %w", err),
			closeWithTimeout(context.Background(), client),
		)
	}

	return NewWithClient(sender, client, ttl)
}
```

Add receiver constructor:

```go
func NewReceiverFromNamespace(namespaceFQDN string, credential azcore.TokenCredential, queueName string) (*Receiver, error) {
	if strings.TrimSpace(queueName) == "" {
		return nil, errors.New("service bus queue name is required")
	}
	client, err := newClientFromNamespace(namespaceFQDN, credential)
	if err != nil {
		return nil, err
	}

	receiver, err := client.NewReceiverForQueue(queueName, &azservicebus.ReceiverOptions{
		ReceiveMode: azservicebus.ReceiveModePeekLock,
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create service bus receiver: %w", err),
			closeWithTimeout(context.Background(), client),
		)
	}

	return NewReceiverWithClient(receiver, client), nil
}
```

In `internal/servicebusqueue/deadletter.go`, add imports:

```go
"strings"

"github.com/Azure/azure-sdk-for-go/sdk/azcore"
```

Add constructor:

```go
func NewDeadLetterQueueFromNamespace(namespaceFQDN string, credential azcore.TokenCredential, queueName string) (*DeadLetterQueue, error) {
	if strings.TrimSpace(queueName) == "" {
		return nil, errors.New("service bus dead-letter queue name is required")
	}
	client, err := newClientFromNamespace(namespaceFQDN, credential)
	if err != nil {
		return nil, err
	}

	sender, err := client.NewSender(queueName, nil)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create service bus dead-letter sender: %w", err),
			closeWithTimeout(context.Background(), client),
		)
	}

	return NewDeadLetterQueueWithClient(sender, client)
}
```

- [ ] **Step 4: Run servicebusqueue tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/servicebusqueue`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/servicebusqueue/queue.go internal/servicebusqueue/deadletter.go internal/servicebusqueue/queue_test.go internal/servicebusqueue/deadletter_test.go
git commit -m "feat: add service bus managed identity constructors"
```

### Task 4: App Wiring For Managed Identity Service Bus

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing app tests with injectable runtime builder**

Add to `internal/app/app_test.go`:

```go
func TestNewUsesManagedIdentityServiceBusRuntime(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusAuthMode = config.ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "nycu-password-hook.servicebus.windows.net"
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	builder := &captureServiceBusRuntimeBuilder{}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New returned nil application")
	}
	if builder.authMode != config.ServiceBusAuthManagedIdentity {
		t.Fatalf("auth mode = %q, want managed_identity", builder.authMode)
	}
	if builder.namespaceFQDN != "nycu-password-hook.servicebus.windows.net" {
		t.Fatalf("namespace = %q", builder.namespaceFQDN)
	}
	if builder.connectionString != "" {
		t.Fatalf("connection string = %q, want empty", builder.connectionString)
	}
}

func TestNewUsesConnectionStringServiceBusRuntime(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusAuthMode = config.ServiceBusAuthConnectionString
	cfg.ServiceBusNamespaceFQDN = ""
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	builder := &captureServiceBusRuntimeBuilder{}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New returned nil application")
	}
	if builder.authMode != config.ServiceBusAuthConnectionString {
		t.Fatalf("auth mode = %q, want connection_string", builder.authMode)
	}
	if builder.connectionString != cfg.ServiceBusConnectionString {
		t.Fatalf("connection string = %q, want config value", builder.connectionString)
	}
}

func TestNewClosesServiceBusRuntimeWhenLaterWiringFails(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PasswordEncryptionKeyB64 = "not-base64"
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	closer := &captureCloser{}
	builder := &captureServiceBusRuntimeBuilder{closers: []appCloser{closer}}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err == nil {
		t.Fatal("New returned nil error")
	}
	if application != nil {
		t.Fatalf("New application = %#v, want nil", application)
	}
	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
}
```

Add helper types:

```go
type captureServiceBusRuntimeBuilder struct {
	authMode         string
	namespaceFQDN    string
	connectionString string
	closers          []appCloser
}

func (b *captureServiceBusRuntimeBuilder) build(cfg config.Config) (serviceBusRuntime, error) {
	b.authMode = cfg.ServiceBusAuthMode
	b.namespaceFQDN = cfg.ServiceBusNamespaceFQDN
	b.connectionString = cfg.ServiceBusConnectionString
	return serviceBusRuntime{
		queue:    &captureQueue{},
		receiver: newBlockingReceiver(),
		dlq:      &captureDeadLetterSink{},
		closers:  b.closers,
	}, nil
}

func replaceServiceBusRuntimeBuilder(fn func(config.Config) (serviceBusRuntime, error)) func() {
	original := buildServiceBusRuntime
	buildServiceBusRuntime = fn
	return func() {
		buildServiceBusRuntime = original
	}
}
```

- [ ] **Step 2: Run app tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: FAIL because `serviceBusRuntime`, `buildServiceBusRuntime`, and managed-identity app wiring do not exist.

- [ ] **Step 3: Extract Service Bus runtime wiring**

In `internal/app/app.go`, add:

```go
type serviceBusRuntime struct {
	queue    migration.Queue
	receiver worker.Receiver
	dlq      worker.DeadLetterSink
	closers  []appCloser
}

var buildServiceBusRuntime = newServiceBusRuntime
```

Replace the three connection-string constructor blocks in `New()` with:

```go
serviceBus, err := buildServiceBusRuntime(cfg)
if err != nil {
	return nil, err
}
closers := append([]appCloser(nil), serviceBus.closers...)
```

Pass `serviceBus.queue`, `serviceBus.receiver`, and `serviceBus.dlq` to `newWithWorkerDependencies()`.

Preserve cleanup behavior for failures after the Service Bus runtime is built. Graph client, processor, password codec, or hook wiring failures must close `serviceBus.closers` through `closeAfterWiringError` or `closeAppResources`, matching the pre-refactor connection-string behavior.

- [ ] **Step 4: Implement auth-mode-specific Service Bus runtime builder**

Add:

```go
func newServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	switch cfg.ServiceBusAuthMode {
	case "", config.ServiceBusAuthConnectionString:
		return newConnectionStringServiceBusRuntime(cfg)
	case config.ServiceBusAuthManagedIdentity:
		return newManagedIdentityServiceBusRuntime(cfg)
	default:
		return serviceBusRuntime{}, errors.New("SERVICEBUS_AUTH_MODE must be connection_string or managed_identity")
	}
}

func newConnectionStringServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	queue, err := servicebusqueue.NewFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return serviceBusRuntime{}, err
	}
	closers := []appCloser{queue}

	receiver, err := servicebusqueue.NewReceiverFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, receiver)

	dlq, err := servicebusqueue.NewDeadLetterQueueFromConnectionString(cfg.ServiceBusConnectionString, cfg.ServiceBusDeadLetterQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, dlq)

	return serviceBusRuntime{queue: queue, receiver: receiver, dlq: dlq, closers: closers}, nil
}

func newManagedIdentityServiceBusRuntime(cfg config.Config) (serviceBusRuntime, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return serviceBusRuntime{}, fmt.Errorf("create Azure credential: %w", err)
	}

	queue, err := servicebusqueue.NewFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusQueueName, cfg.PasswordMessageTTL)
	if err != nil {
		return serviceBusRuntime{}, err
	}
	closers := []appCloser{queue}

	receiver, err := servicebusqueue.NewReceiverFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, receiver)

	dlq, err := servicebusqueue.NewDeadLetterQueueFromNamespace(cfg.ServiceBusNamespaceFQDN, credential, cfg.ServiceBusDeadLetterQueueName)
	if err != nil {
		return serviceBusRuntime{}, closeAfterWiringError(err, closers)
	}
	closers = append(closers, dlq)

	return serviceBusRuntime{queue: queue, receiver: receiver, dlq: dlq, closers: closers}, nil
}
```

Add `fmt` import to `internal/app/app.go`.

- [ ] **Step 5: Update test config helper**

In `completeAppConfig()` in `internal/app/app_test.go`, add:

```go
ServiceBusAuthMode:      config.ServiceBusAuthConnectionString,
ServiceBusNamespaceFQDN: "",
```

- [ ] **Step 6: Run app tests and verify they pass**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire service bus managed identity auth"
```

### Task 5: Documentation And Infrastructure Draft Handoff

**Files:**
- Modify: `README.md`
- Optionally modify: `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`

- [ ] **Step 1: Update README configuration**

In `README.md`, add production managed identity settings to the local run/config sections:

```bash
export SERVICEBUS_AUTH_MODE="managed_identity"
export SERVICEBUS_NAMESPACE_FQDN="<namespace>.servicebus.windows.net"
export SERVICEBUS_QUEUE_NAME="password-sync"
export SERVICEBUS_DEADLETTER_QUEUE_NAME="password-sync-dlq"
```

Document local fallback:

```bash
export SERVICEBUS_AUTH_MODE="connection_string"
export SERVICEBUS_CONNECTION_STRING="<redacted-service-bus-connection-string>"
```

Update the configuration table rows:

```markdown
| `SERVICEBUS_AUTH_MODE` | `connection_string` | `connection_string` for local/rollback or `managed_identity` for Azure production |
| `SERVICEBUS_NAMESPACE_FQDN` | empty | Required when `SERVICEBUS_AUTH_MODE=managed_identity`; Service Bus namespace host such as `<name>.servicebus.windows.net` |
| `SERVICEBUS_CONNECTION_STRING` | empty | Required only when `SERVICEBUS_AUTH_MODE=connection_string`; not required in managed identity mode |
```

- [ ] **Step 2: Document RBAC expectations**

Add a short Service Bus Managed Identity section:

```markdown
## Service Bus Managed Identity

Production should use `SERVICEBUS_AUTH_MODE=managed_identity` so the Container App authenticates to Service Bus with its Azure managed identity instead of a long-lived Service Bus connection string.

The managed identity needs permissions to:

- send to the active password sync queue
- receive and settle messages from the active password sync queue
- send to the application-level safe DLQ queue

Infrastructure should grant the narrowest supported Azure Service Bus RBAC roles for those operations. If queue-scoped RBAC is not available in the target environment, use the smallest acceptable scope and document the tradeoff.
```

- [ ] **Step 3: Refresh infrastructure draft if this slice is accepted**

If this draft is promoted or the owner asks for draft consistency now, update `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`:

```markdown
Service Bus production auth uses Managed Identity. Terraform assigns the Container App managed identity Azure Service Bus RBAC for send/listen operations and does not create or inject `servicebus-conn-str` in the preferred production path.
```

Keep connection-string fallback documented only as local/rollback, not the preferred infrastructure path.

- [ ] **Step 4: Run docs scan**

Run:

```bash
rg -n "SERVICEBUS_AUTH_MODE|SERVICEBUS_NAMESPACE_FQDN|SERVICEBUS_CONNECTION_STRING|servicebus-conn-str|Managed Identity" README.md docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md
```

Expected:
- `SERVICEBUS_AUTH_MODE` and `SERVICEBUS_NAMESPACE_FQDN` are documented.
- `SERVICEBUS_CONNECTION_STRING` is not described as required for managed identity production.
- `servicebus-conn-str` remains only in connection-string fallback or older-draft compatibility notes.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md
git commit -m "docs: document service bus managed identity auth"
```

### Task 6: Full Verification

**Files:**
- Verify all touched Go and docs files

- [ ] **Step 1: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/config ./internal/secretloader ./internal/servicebusqueue ./internal/app
```

Expected: PASS.

- [ ] **Step 2: Run full Go verification**

Run:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

Expected: PASS.

- [ ] **Step 3: Run secret leakage scan**

Run:

```bash
rg -n "SERVICEBUS_CONNECTION_STRING=.*SharedAccessKey|SharedAccessKey=|RootManageSharedAccessKey|servicebus-conn-str" --glob '!docs/superpowers/plans/**'
```

Expected:
- No real Service Bus key values.
- `servicebus-conn-str` appears only in docs or tests as a secret name/fallback reference.

- [ ] **Step 4: Run scope check**

Run:

```bash
git diff --name-only HEAD~5..HEAD
```

Expected changed paths are limited to:

```text
README.md
docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md
internal/app/app.go
internal/app/app_test.go
internal/config/config.go
internal/config/config_test.go
internal/secretloader/loader.go
internal/secretloader/loader_test.go
internal/servicebusqueue/deadletter.go
internal/servicebusqueue/deadletter_test.go
internal/servicebusqueue/queue.go
internal/servicebusqueue/queue_test.go
```

No Terraform resources should be implemented in this slice.

---

## Self-Review

- Spec coverage: This draft covers the requested managed-identity conversion: config, Key Vault loading, Service Bus constructors, app wiring, tests, docs, and infrastructure handoff.
- Boundary check: This draft does not implement Terraform or Azure RBAC. It prepares the application for the later infrastructure slice.
- Backward compatibility: Connection-string mode remains available for local development and rollback.
- Secret-state check: Managed identity mode removes the production need for `SERVICEBUS_CONNECTION_STRING` and `servicebus-conn-str`.
- Type consistency: New names are consistent across tasks: `ServiceBusAuthMode`, `ServiceBusAuthConnectionString`, `ServiceBusAuthManagedIdentity`, `ServiceBusNamespaceFQDN`, `SERVICEBUS_AUTH_MODE`, and `SERVICEBUS_NAMESPACE_FQDN`.
