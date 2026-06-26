# Secret Loading Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load runtime secrets through Azure Key Vault with Managed Identity for production while requiring an explicit local `SECRETS_SOURCE=env` fallback for development and tests.

**Architecture:** Keep config shape and validation in `internal/config`, and add `internal/secretloader` as the only package that resolves secret values. `cmd/server` loads environment config, resolves secrets, then passes a fully resolved `config.Config` to `app.New`; existing HTTP and Service Bus wiring continue to receive plain config values. Slice 3 adds Graph credential config only as resolved runtime configuration, leaving Graph API behavior to a later slice.

**Tech Stack:** Go 1.26, Azure SDK for Go `github.com/Azure/azure-sdk-for-go/sdk/azidentity`, Azure SDK for Go `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets`, existing Dockerized Makefile verification.

**Reference APIs verified on 2026-06-26:**
- `azidentity.NewDefaultAzureCredential(nil)` creates the credential chain and includes managed identity for production.
- `azsecrets.NewClient(vaultURL, credential, nil)` creates a Key Vault Secrets client.
- `client.GetSecret(ctx, name, "", nil)` reads the latest secret version; `resp.Value` is a `*string`.

---

## File Structure

- Modify: `go.mod` and `go.sum` - add Azure identity and Key Vault Secrets SDK dependencies.
- Modify: `internal/config/config.go` - add explicit secret source, Key Vault URL/name settings, Graph credential fields, fallback domain, and split validation for unresolved versus resolved config.
- Modify: `internal/config/config_test.go` - cover explicit local fallback, Key Vault input validation, Graph credential validation, and default Key Vault secret names.
- Create: `internal/secretloader/loader.go` - resolve `env` versus `keyvault` secret values and expose an injectable getter for tests.
- Create: `internal/secretloader/loader_test.go` - verify local env mode, Key Vault resolution, missing/blank secret errors, and no secret value leakage in errors.
- Modify: `cmd/server/main.go` - resolve secrets before constructing the app.
- Modify: `internal/app/app_test.go` - update complete config helper with newly required resolved fields.
- Modify: `README.md` - document `SECRETS_SOURCE=env`, `SECRETS_SOURCE=keyvault`, Key Vault secret names, Graph credential variables, and local run commands.
- Modify: `deploy/docker-compose.yml` - make local fallback explicit and include required local development values.
- Modify: `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md` - mark Slice 3 as planned/active during implementation and done after verification.

---

### Task 1: Add Explicit Secret Loading Config Contract

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing config tests**

Add the following tests to `internal/config/config_test.go`:

```go
func TestValidateRequiresExplicitSecretsSource(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = ""

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "SECRETS_SOURCE is required (env or keyvault)" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "SECRETS_SOURCE is required (env or keyvault)")
	}
}

func TestValidateRejectsUnknownSecretsSource(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = "file"

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "SECRETS_SOURCE must be env or keyvault" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "SECRETS_SOURCE must be env or keyvault")
	}
}

func TestValidateKeyVaultSourceRequiresVaultURL(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = SecretsSourceKeyVault
	cfg.KeyVaultURL = ""

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "KEY_VAULT_URL is required when SECRETS_SOURCE=keyvault" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "KEY_VAULT_URL is required when SECRETS_SOURCE=keyvault")
	}
}

func TestValidateKeyVaultSourceRequiresHTTPSVaultURL(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = SecretsSourceKeyVault
	cfg.KeyVaultURL = "http://vault.example"

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "KEY_VAULT_URL must start with https://" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "KEY_VAULT_URL must start with https://")
	}
}

func TestValidateRequiresGraphCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{
			name: "tenant",
			edit: func(cfg *Config) { cfg.GraphTenantID = "" },
			want: "GRAPH_TENANT_ID is required",
		},
		{
			name: "client id",
			edit: func(cfg *Config) { cfg.GraphClientID = "" },
			want: "GRAPH_CLIENT_ID is required",
		},
		{
			name: "client secret",
			edit: func(cfg *Config) { cfg.GraphClientSecret = "" },
			want: "GRAPH_CLIENT_SECRET is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := completeConfig()
			tt.edit(&cfg)

			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadSecretLoadingDefaults(t *testing.T) {
	t.Setenv("SECRETS_SOURCE", "keyvault")
	t.Setenv("KEY_VAULT_URL", " https://nycu-password-hook.vault.azure.net/ ")
	t.Setenv("HOOK_HMAC_SECRET", "")
	t.Setenv("SERVICEBUS_CONNECTION_STRING", "")
	t.Setenv("GRAPH_TENANT_ID", "tenant-id")
	t.Setenv("GRAPH_CLIENT_ID", "client-id")
	t.Setenv("GRAPH_CLIENT_SECRET", "")

	cfg := Load()

	if cfg.SecretsSource != SecretsSourceKeyVault {
		t.Fatalf("SecretsSource = %q, want %q", cfg.SecretsSource, SecretsSourceKeyVault)
	}
	if cfg.KeyVaultURL != "https://nycu-password-hook.vault.azure.net/" {
		t.Fatalf("KeyVaultURL = %q", cfg.KeyVaultURL)
	}
	if cfg.KeyVaultSecretNames.HMACSecret != "hook-hmac-secret" {
		t.Fatalf("HMACSecret name = %q", cfg.KeyVaultSecretNames.HMACSecret)
	}
	if cfg.KeyVaultSecretNames.ServiceBusConnectionString != "servicebus-conn-str" {
		t.Fatalf("ServiceBusConnectionString name = %q", cfg.KeyVaultSecretNames.ServiceBusConnectionString)
	}
	if cfg.KeyVaultSecretNames.GraphClientSecret != "graph-client-secret" {
		t.Fatalf("GraphClientSecret name = %q", cfg.KeyVaultSecretNames.GraphClientSecret)
	}
	if cfg.GraphTenantID != "tenant-id" || cfg.GraphClientID != "client-id" {
		t.Fatalf("Graph tenant/client = %q/%q", cfg.GraphTenantID, cfg.GraphClientID)
	}
}
```

Update `completeConfig()` in `internal/config/config_test.go` and `completeAppConfig()` in `internal/app/app_test.go` to include:

```go
SecretsSource:               config.SecretsSourceEnv,
KeyVaultURL:                 "",
KeyVaultSecretNames: config.KeyVaultSecretNames{
	HMACSecret:                 "hook-hmac-secret",
	ServiceBusConnectionString: "servicebus-conn-str",
	GraphClientSecret:         "graph-client-secret",
},
EntraFallbackDomain:         "nycu.onmicrosoft.com",
GraphTenantID:               "tenant-id",
GraphClientID:               "client-id",
GraphClientSecret:           "graph-client-secret",
```

In `internal/config/config_test.go`, omit the `config.` qualifier because the tests are in package `config`.

- [ ] **Step 2: Run config tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/config ./internal/app
```

Expected: FAIL because `Config` does not yet define `SecretsSource`, `KeyVaultURL`, `KeyVaultSecretNames`, `GraphTenantID`, `GraphClientID`, `GraphClientSecret`, or `ValidateSecretLoadingInputs`.

- [ ] **Step 3: Implement config fields and validation**

In `internal/config/config.go`, add constants and types near the top of the file:

```go
const (
	SecretsSourceEnv      = "env"
	SecretsSourceKeyVault = "keyvault"
)

type KeyVaultSecretNames struct {
	HMACSecret                 string
	ServiceBusConnectionString string
	GraphClientSecret         string
}
```

Extend `Config`:

```go
SecretsSource               string
KeyVaultURL                 string
KeyVaultSecretNames         KeyVaultSecretNames
EntraFallbackDomain         string
GraphTenantID               string
GraphClientID               string
GraphClientSecret           string
```

Update `Load()` with these fields:

```go
SecretsSource: strings.TrimSpace(os.Getenv("SECRETS_SOURCE")),
KeyVaultURL:   strings.TrimSpace(os.Getenv("KEY_VAULT_URL")),
KeyVaultSecretNames: KeyVaultSecretNames{
	HMACSecret:                 env("KEY_VAULT_HMAC_SECRET_NAME", "hook-hmac-secret"),
	ServiceBusConnectionString: env("KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME", "servicebus-conn-str"),
	GraphClientSecret:         env("KEY_VAULT_GRAPH_CLIENT_SECRET_NAME", "graph-client-secret"),
},
EntraFallbackDomain: strings.TrimSpace(os.Getenv("ENTRA_FALLBACK_DOMAIN")),
GraphTenantID:       strings.TrimSpace(os.Getenv("GRAPH_TENANT_ID")),
GraphClientID:       strings.TrimSpace(os.Getenv("GRAPH_CLIENT_ID")),
GraphClientSecret:   strings.TrimSpace(os.Getenv("GRAPH_CLIENT_SECRET")),
```

Add `ValidateSecretLoadingInputs()`:

```go
func (c Config) ValidateSecretLoadingInputs() error {
	switch c.SecretsSource {
	case "":
		return errors.New("SECRETS_SOURCE is required (env or keyvault)")
	case SecretsSourceEnv:
		return nil
	case SecretsSourceKeyVault:
		if strings.TrimSpace(c.KeyVaultURL) == "" {
			return errors.New("KEY_VAULT_URL is required when SECRETS_SOURCE=keyvault")
		}
		if !strings.HasPrefix(c.KeyVaultURL, "https://") {
			return errors.New("KEY_VAULT_URL must start with https://")
		}
		switch {
		case strings.TrimSpace(c.KeyVaultSecretNames.HMACSecret) == "":
			return errors.New("KEY_VAULT_HMAC_SECRET_NAME is required when SECRETS_SOURCE=keyvault")
		case strings.TrimSpace(c.KeyVaultSecretNames.ServiceBusConnectionString) == "":
			return errors.New("KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME is required when SECRETS_SOURCE=keyvault")
		case strings.TrimSpace(c.KeyVaultSecretNames.GraphClientSecret) == "":
			return errors.New("KEY_VAULT_GRAPH_CLIENT_SECRET_NAME is required when SECRETS_SOURCE=keyvault")
		default:
			return nil
		}
	default:
		return errors.New("SECRETS_SOURCE must be env or keyvault")
	}
}
```

Update the start of `Validate()`:

```go
if err := c.ValidateSecretLoadingInputs(); err != nil {
	return err
}
```

Add these cases after `ENTRA_PRIMARY_DOMAIN` validation:

```go
case strings.Contains(c.EntraFallbackDomain, "@"):
	return fmt.Errorf("ENTRA_FALLBACK_DOMAIN must be a domain, got %q", c.EntraFallbackDomain)
case strings.TrimSpace(c.GraphTenantID) == "":
	return errors.New("GRAPH_TENANT_ID is required")
case strings.TrimSpace(c.GraphClientID) == "":
	return errors.New("GRAPH_CLIENT_ID is required")
case strings.TrimSpace(c.GraphClientSecret) == "":
	return errors.New("GRAPH_CLIENT_SECRET is required")
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/config ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/app/app_test.go
git commit -m "feat: define explicit secret loading config"
```

---

### Task 2: Add Secret Loader With Injectable Key Vault Getter

**Files:**
- Create: `internal/secretloader/loader.go`
- Create: `internal/secretloader/loader_test.go`

- [ ] **Step 1: Write failing loader tests**

Create `internal/secretloader/loader_test.go`:

```go
package secretloader

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/config"
)

func TestResolveEnvSourceUsesAlreadyLoadedConfig(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	getter := &fakeGetter{values: map[string]string{
		"hook-hmac-secret": "from-key-vault",
	}}

	got, err := Resolve(context.Background(), cfg, getter)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.HMACSecret != "shared-secret" {
		t.Fatalf("HMACSecret = %q, want env value", got.HMACSecret)
	}
	if getter.calls != nil {
		t.Fatalf("getter was called in env mode: %v", getter.calls)
	}
}

func TestResolveKeyVaultSourceLoadsSecretValues(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = config.SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.HMACSecret = ""
	cfg.ServiceBusConnectionString = ""
	cfg.GraphClientSecret = ""
	getter := &fakeGetter{values: map[string]string{
		"hook-hmac-secret":     "kv-hmac",
		"servicebus-conn-str":  "kv-servicebus",
		"graph-client-secret":  "kv-graph-secret",
	}}

	got, err := Resolve(context.Background(), cfg, getter)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if got.HMACSecret != "kv-hmac" {
		t.Fatalf("HMACSecret = %q", got.HMACSecret)
	}
	if got.ServiceBusConnectionString != "kv-servicebus" {
		t.Fatalf("ServiceBusConnectionString = %q", got.ServiceBusConnectionString)
	}
	if got.GraphClientSecret != "kv-graph-secret" {
		t.Fatalf("GraphClientSecret = %q", got.GraphClientSecret)
	}
	wantCalls := []string{"hook-hmac-secret", "servicebus-conn-str", "graph-client-secret"}
	if strings.Join(getter.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("calls = %v, want %v", getter.calls, wantCalls)
	}
}

func TestResolveKeyVaultSourceWrapsGetterErrorWithoutSecretValue(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = config.SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.HMACSecret = ""
	cfg.ServiceBusConnectionString = ""
	cfg.GraphClientSecret = ""
	getter := &fakeGetter{
		values: map[string]string{
			"hook-hmac-secret": "kv-hmac",
		},
		errs: map[string]error{
			"servicebus-conn-str": errors.New("permission denied for secret value kv-servicebus"),
		},
	}

	_, err := Resolve(context.Background(), cfg, getter)
	if err == nil {
		t.Fatal("Resolve returned nil error")
	}
	if !strings.Contains(err.Error(), "load Key Vault secret servicebus-conn-str") {
		t.Fatalf("error = %v, want secret name context", err)
	}
	if strings.Contains(err.Error(), "kv-servicebus") {
		t.Fatalf("error leaked secret value: %v", err)
	}
}

func TestResolveKeyVaultSourceRejectsBlankSecretValue(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = config.SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.HMACSecret = ""
	cfg.ServiceBusConnectionString = ""
	cfg.GraphClientSecret = ""
	getter := &fakeGetter{values: map[string]string{
		"hook-hmac-secret":    "kv-hmac",
		"servicebus-conn-str": "   ",
	}}

	_, err := Resolve(context.Background(), cfg, getter)
	if err == nil || err.Error() != "load Key Vault secret servicebus-conn-str: secret value is empty" {
		t.Fatalf("Resolve error = %v, want blank secret error", err)
	}
}

type fakeGetter struct {
	values map[string]string
	errs   map[string]error
	calls  []string
}

func (g *fakeGetter) GetSecret(ctx context.Context, name string) (string, error) {
	g.calls = append(g.calls, name)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := g.errs[name]; err != nil {
		return "", err
	}
	return g.values[name], nil
}

func completeConfig() config.Config {
	return config.Config{
		SecretsSource:               config.SecretsSourceEnv,
		KeyVaultURL:                 "",
		KeyVaultSecretNames: config.KeyVaultSecretNames{
			HMACSecret:                 "hook-hmac-secret",
			ServiceBusConnectionString: "servicebus-conn-str",
			GraphClientSecret:         "graph-client-secret",
		},
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		EntraFallbackDomain:        "nycu.onmicrosoft.com",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		PortalAllowedCIDRs:         nil,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
		GraphTenantID:              "tenant-id",
		GraphClientID:              "client-id",
		GraphClientSecret:          "graph-client-secret",
	}
}
```

- [ ] **Step 2: Run loader tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/secretloader
```

Expected: FAIL because `internal/secretloader` does not exist.

- [ ] **Step 3: Implement loader without Azure SDK adapter**

Create `internal/secretloader/loader.go`:

```go
package secretloader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nycu/password-hook-service/internal/config"
)

type Getter interface {
	GetSecret(context.Context, string) (string, error)
}

func Resolve(ctx context.Context, cfg config.Config, getter Getter) (config.Config, error) {
	if err := cfg.ValidateSecretLoadingInputs(); err != nil {
		return config.Config{}, err
	}

	switch cfg.SecretsSource {
	case config.SecretsSourceEnv:
		return cfg, nil
	case config.SecretsSourceKeyVault:
		if getter == nil {
			return config.Config{}, errors.New("key vault getter is required")
		}
		return resolveKeyVault(ctx, cfg, getter)
	default:
		return config.Config{}, errors.New("SECRETS_SOURCE must be env or keyvault")
	}
}

func resolveKeyVault(ctx context.Context, cfg config.Config, getter Getter) (config.Config, error) {
	hmacSecret, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.HMACSecret)
	if err != nil {
		return config.Config{}, err
	}
	serviceBusConnectionString, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.ServiceBusConnectionString)
	if err != nil {
		return config.Config{}, err
	}
	graphClientSecret, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.GraphClientSecret)
	if err != nil {
		return config.Config{}, err
	}

	cfg.HMACSecret = hmacSecret
	cfg.ServiceBusConnectionString = serviceBusConnectionString
	cfg.GraphClientSecret = graphClientSecret
	return cfg, nil
}

func getRequiredSecret(ctx context.Context, getter Getter, name string) (string, error) {
	value, err := getter.GetSecret(ctx, name)
	if err != nil {
		return "", fmt.Errorf("load Key Vault secret %s: %w", name, sanitizeSecretError(err))
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("load Key Vault secret %s: secret value is empty", name)
	}
	return value, nil
}

func sanitizeSecretError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("secret read failed")
}
```

- [ ] **Step 4: Run loader tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/secretloader
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secretloader/loader.go internal/secretloader/loader_test.go
git commit -m "feat: add secret loading resolver"
```

---

### Task 3: Add Azure Key Vault Getter

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/secretloader/loader.go`
- Create: `internal/secretloader/azure_test.go`

- [ ] **Step 1: Add Azure SDK dependencies**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go get github.com/Azure/azure-sdk-for-go/sdk/azidentity github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets
```

Expected: `go.mod` includes direct requirements for `azidentity` and `azsecrets`; `go.sum` is updated.

- [ ] **Step 2: Write Azure getter unit test for nil secret values**

Create `internal/secretloader/azure_test.go`:

```go
package secretloader

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func TestKeyVaultGetterRejectsNilSecretValue(t *testing.T) {
	t.Parallel()

	getter := keyVaultGetter{client: fakeKeyVaultClient{}}

	_, err := getter.GetSecret(context.Background(), "hook-hmac-secret")
	if err == nil || err.Error() != "Key Vault secret hook-hmac-secret has nil value" {
		t.Fatalf("GetSecret error = %v, want nil value error", err)
	}
}

type fakeKeyVaultClient struct{}

func (fakeKeyVaultClient) GetSecret(context.Context, string, string, *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	return azsecrets.GetSecretResponse{}, nil
}
```

- [ ] **Step 3: Run Azure getter test to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/secretloader
```

Expected: FAIL because `keyVaultGetter` does not exist.

- [ ] **Step 4: Implement Azure Key Vault getter**

Replace `internal/secretloader/loader.go` with an implementation that keeps the existing resolver code and adds this Azure adapter:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/nycu/password-hook-service/internal/config"
)

type keyVaultClient interface {
	GetSecret(context.Context, string, string, *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error)
}

type keyVaultGetter struct {
	client keyVaultClient
}

func NewKeyVaultGetter(vaultURL string) (Getter, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	client, err := azsecrets.NewClient(vaultURL, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Key Vault secrets client: %w", err)
	}
	return keyVaultGetter{client: client}, nil
}

func (g keyVaultGetter) GetSecret(ctx context.Context, name string) (string, error) {
	resp, err := g.client.GetSecret(ctx, name, "", nil)
	if err != nil {
		return "", err
	}
	if resp.Value == nil {
		return "", fmt.Errorf("Key Vault secret %s has nil value", name)
	}
	return *resp.Value, nil
}
```

Update `Resolve()` so production can create the Azure getter when `getter == nil`:

```go
case config.SecretsSourceKeyVault:
	if getter == nil {
		var err error
		getter, err = NewKeyVaultGetter(cfg.KeyVaultURL)
		if err != nil {
			return config.Config{}, err
		}
	}
	return resolveKeyVault(ctx, cfg, getter)
```

- [ ] **Step 5: Run loader tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/secretloader
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/secretloader/loader.go internal/secretloader/azure_test.go
git commit -m "feat: load secrets from Azure Key Vault"
```

---

### Task 4: Wire Secret Resolution Into Server Startup

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write the startup wiring change**

Update `cmd/server/main.go` imports to include:

```go
"github.com/nycu/password-hook-service/internal/secretloader"
```

Replace:

```go
application, err := app.New(config.Load())
if err != nil {
	slog.Error("invalid configuration", slog.Any("error", err))
	os.Exit(1)
}
```

with:

```go
cfg, err := secretloader.Resolve(ctx, config.Load(), nil)
if err != nil {
	slog.Error("load configuration", slog.Any("error", err))
	os.Exit(1)
}

application, err := app.New(cfg)
if err != nil {
	slog.Error("invalid configuration", slog.Any("error", err))
	os.Exit(1)
}
```

- [ ] **Step 2: Run server package tests/build**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./cmd/server ./internal/app ./internal/config ./internal/secretloader
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: resolve runtime secrets at startup"
```

---

### Task 5: Document Local and Key Vault Configuration

**Files:**
- Modify: `README.md`
- Modify: `deploy/docker-compose.yml`
- Modify: `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md`

- [ ] **Step 1: Update README current scope**

In `README.md`, change the current scope paragraph from:

```markdown
Microsoft Graph, Key Vault, worker consumption, retry/DLQ policy, Terraform resources, and CI/CD security gates are implemented in later slices.
```

to:

```markdown
Microsoft Graph, worker consumption, retry/DLQ policy, Terraform resources, and CI/CD security gates are implemented in later slices.
```

Add a bullet to the implemented list:

```markdown
- explicit runtime secret loading from local env or Azure Key Vault
```

- [ ] **Step 2: Update README local env**

Replace the local environment block with:

```bash
export SECRETS_SOURCE="env"
export HOOK_HMAC_SECRET="local-development-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export ENTRA_FALLBACK_DOMAIN="nycu.onmicrosoft.com"
export GRAPH_TENANT_ID="00000000-0000-0000-0000-000000000000"
export GRAPH_CLIENT_ID="11111111-1111-1111-1111-111111111111"
export GRAPH_CLIENT_SECRET="local-graph-client-secret"
export PROBLEM_BASE_URL="https://nycu.edu.tw/problems"
export HTTP_ADDR=":8080"
export SERVICEBUS_CONNECTION_STRING="Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA=="
export SERVICEBUS_QUEUE_NAME="password-sync"
```

Update the `docker run` env list to include:

```bash
  -e SECRETS_SOURCE \
  -e ENTRA_FALLBACK_DOMAIN \
  -e GRAPH_TENANT_ID \
  -e GRAPH_CLIENT_ID \
  -e GRAPH_CLIENT_SECRET \
```

- [ ] **Step 3: Add README Key Vault mode**

Add this section after local run:

````markdown
## Azure Key Vault Secret Loading

Production uses Managed Identity through Azure SDK `DefaultAzureCredential`.

```bash
export SECRETS_SOURCE="keyvault"
export KEY_VAULT_URL="https://<vault-name>.vault.azure.net/"
export KEY_VAULT_HMAC_SECRET_NAME="hook-hmac-secret"
export KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME="servicebus-conn-str"
export KEY_VAULT_GRAPH_CLIENT_SECRET_NAME="graph-client-secret"
export ENTRA_PRIMARY_DOMAIN="nycu.edu.tw"
export ENTRA_FALLBACK_DOMAIN="nycu.onmicrosoft.com"
export GRAPH_TENANT_ID="<tenant-id>"
export GRAPH_CLIENT_ID="<app-client-id>"
export SERVICEBUS_QUEUE_NAME="password-sync"
```

The managed identity assigned to the container app must have `secrets/get` permission for the configured Key Vault. Local development must opt into `SECRETS_SOURCE=env`; the service does not silently fall back from Key Vault to environment secrets.
````

- [ ] **Step 4: Update README configuration table**

Replace the configuration table with:

```markdown
| Variable | Default | Purpose |
|----------|---------|---------|
| `SECRETS_SOURCE` | empty | Required; `env` for explicit local fallback or `keyvault` for Azure Key Vault |
| `KEY_VAULT_URL` | empty | Required when `SECRETS_SOURCE=keyvault` |
| `KEY_VAULT_HMAC_SECRET_NAME` | `hook-hmac-secret` | Key Vault secret name for the HMAC shared secret |
| `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME` | `servicebus-conn-str` | Key Vault secret name for the Service Bus connection string |
| `KEY_VAULT_GRAPH_CLIENT_SECRET_NAME` | `graph-client-secret` | Key Vault secret name for the Graph client secret |
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOOK_HMAC_SECRET` | empty | HMAC shared secret when `SECRETS_SOURCE=env` |
| `ENTRA_PRIMARY_DOMAIN` | `nycu.edu.tw` | Domain used to build internal Entra UPNs |
| `ENTRA_FALLBACK_DOMAIN` | empty | Optional fallback domain for later tenant bootstrap scenarios |
| `GRAPH_TENANT_ID` | empty | Microsoft Entra tenant ID for later Graph client use |
| `GRAPH_CLIENT_ID` | empty | App registration client ID for later Graph client use |
| `GRAPH_CLIENT_SECRET` | empty | Graph app client secret when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `PROBLEM_BASE_URL` | `https://nycu.edu.tw/problems` | RFC 9457 problem type base URL |
| `SERVICEBUS_CONNECTION_STRING` | empty | Azure Service Bus connection string when `SECRETS_SOURCE=env`; loaded from Key Vault when `SECRETS_SOURCE=keyvault` |
| `SERVICEBUS_QUEUE_NAME` | `password-sync` | Queue name for password sync jobs |
| `PORTAL_ALLOWED_CIDRS` | empty | Optional comma-separated source CIDR allowlist |
| `RATE_LIMIT_PER_IP` | `500` | Per-IP request threshold per one-second window |
```

- [ ] **Step 5: Update docker-compose local fallback**

Update `deploy/docker-compose.yml` service environment:

```yaml
environment:
  HTTP_ADDR: ":8080"
  SECRETS_SOURCE: "env"
  HOOK_HMAC_SECRET: "local-development-secret"
  ENTRA_PRIMARY_DOMAIN: "nycu.edu.tw"
  ENTRA_FALLBACK_DOMAIN: "nycu.onmicrosoft.com"
  GRAPH_TENANT_ID: "00000000-0000-0000-0000-000000000000"
  GRAPH_CLIENT_ID: "11111111-1111-1111-1111-111111111111"
  GRAPH_CLIENT_SECRET: "local-graph-client-secret"
  PROBLEM_BASE_URL: "https://nycu.edu.tw/problems"
  SERVICEBUS_CONNECTION_STRING: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA=="
  SERVICEBUS_QUEUE_NAME: "password-sync"
```

- [ ] **Step 6: Update roadmap active slice**

In `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md`, change the active plan to:

```markdown
- Slice 3: `docs/superpowers/plans/2026-06-26-secret-loading.md`
```

Update the Slice 3 row:

```markdown
| 3. Secret Loading | Planned | `2026-06-26-secret-loading.md` | Loads runtime secrets via Key Vault/Managed Identity with explicit local env fallback |
```

- [ ] **Step 7: Commit**

```bash
git add README.md deploy/docker-compose.yml docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md
git commit -m "docs: document secret loading configuration"
```

---

### Task 6: Full Verification

**Files:**
- Verify all modified files.

- [ ] **Step 1: Run full verification**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."
```

Expected: PASS.

- [ ] **Step 2: Confirm no secrets are hardcoded beyond test/local examples**

Run:

```bash
rg -n "local-graph-client-secret|local-development-secret|SharedAccessKey=dGVzdA==|kv-servicebus|kv-graph-secret" .
```

Expected: matches appear only in tests, README local examples, and docker-compose local examples.

- [ ] **Step 3: Confirm roadmap and plan status**

Run:

```bash
git status --short --branch
git log --oneline -10
```

Expected: branch remains the active slice branch or the executor-created slice 3 branch; commits include the five Slice 3 commits above; no unrelated files are modified.

- [ ] **Step 4: Mark Slice 3 done after verification**

After all tests pass and review fixes are applied, update `docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md`:

```markdown
| 3. Secret Loading | Done | `2026-06-26-secret-loading.md` | Key Vault/Managed Identity secret loading verified; explicit local env fallback documented |
```

- [ ] **Step 5: Final commit**

```bash
git add docs/superpowers/plans/2026-06-24-password-hook-service-roadmap.md
git commit -m "docs: mark secret loading slice complete"
```

---

## Self-Review

**Spec coverage:** This plan covers Key Vault secret loading with Managed Identity, the HMAC secret, Service Bus connection string, Graph client secret, Graph tenant/client runtime config, tenant domain config, and explicit local development fallback. It intentionally does not implement Graph API calls, worker consumption, password zeroing, retry/DLQ policy, or Terraform Key Vault resources; those belong to later roadmap slices.

**Placeholder scan:** No steps rely on TBD/TODO/fill-in behavior. The only angle-bracket examples are README placeholders for real Azure tenant/vault values that must differ by environment.

**Type consistency:** `config.KeyVaultSecretNames`, `config.SecretsSourceEnv`, `config.SecretsSourceKeyVault`, `secretloader.Getter`, `secretloader.Resolve`, and `secretloader.NewKeyVaultGetter` are introduced before later tasks reference them.
