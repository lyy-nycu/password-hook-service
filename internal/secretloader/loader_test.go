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
		"hook-hmac-secret":    "kv-hmac",
		"servicebus-conn-str": "kv-servicebus",
		"graph-client-secret": "kv-graph-secret",
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
		SecretsSource:              config.SecretsSourceEnv,
		KeyVaultURL:                "",
		KeyVaultSecretNames:        config.KeyVaultSecretNames{HMACSecret: "hook-hmac-secret", ServiceBusConnectionString: "servicebus-conn-str", GraphClientSecret: "graph-client-secret"},
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
