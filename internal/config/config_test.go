package config

import (
	"testing"
	"time"
)

const testServiceBusConnectionString = "servicebus-connection-string-for-tests"

func TestValidateRequiresHMACSecret(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.HMACSecret = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without HMAC secret")
	}
}

func TestValidateAcceptsCompleteConfig(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestLoadServiceBusDefaults(t *testing.T) {
	t.Setenv("HOOK_HMAC_SECRET", "shared-secret")
	t.Setenv("SERVICEBUS_CONNECTION_STRING", " "+testServiceBusConnectionString+" ")
	t.Setenv("SERVICEBUS_QUEUE_NAME", "")

	cfg := Load()

	if cfg.ServiceBusConnectionString != testServiceBusConnectionString {
		t.Fatalf("ServiceBusConnectionString = %q", cfg.ServiceBusConnectionString)
	}
	if cfg.ServiceBusQueueName != "password-sync" {
		t.Fatalf("ServiceBusQueueName = %q, want password-sync", cfg.ServiceBusQueueName)
	}
	if cfg.PasswordMessageTTL != 300*time.Second {
		t.Fatalf("PasswordMessageTTL = %s, want 300s", cfg.PasswordMessageTTL)
	}
}

func TestValidateRejectsInvalidPortalAllowedCIDR(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PortalAllowedCIDRs = []string{"not-a-cidr"}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error for invalid PORTAL_ALLOWED_CIDRS")
	}
}

func TestValidateRequiresServiceBusConnectionString(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusConnectionString = ""

	if err := cfg.Validate(); err == nil || err.Error() != "SERVICEBUS_CONNECTION_STRING is required" {
		t.Fatalf("Validate error = %v, want %q", err, "SERVICEBUS_CONNECTION_STRING is required")
	}
}

func TestValidateRequiresServiceBusQueueName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusQueueName = ""

	if err := cfg.Validate(); err == nil || err.Error() != "SERVICEBUS_QUEUE_NAME is required" {
		t.Fatalf("Validate error = %v, want %q", err, "SERVICEBUS_QUEUE_NAME is required")
	}
}

func TestValidateRequiresPositivePasswordMessageTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ttl  time.Duration
	}{
		{name: "zero", ttl: 0},
		{name: "negative", ttl: -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := completeConfig()
			cfg.PasswordMessageTTL = tt.ttl

			if err := cfg.Validate(); err == nil || err.Error() != "PasswordMessageTTL must be positive" {
				t.Fatalf("Validate error = %v, want %q", err, "PasswordMessageTTL must be positive")
			}
		})
	}
}

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

func TestValidateKeyVaultSourceRequiresGraphClientSecretName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.KeyVaultSecretNames.GraphClientSecret = ""

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "KEY_VAULT_GRAPH_CLIENT_SECRET_NAME is required when SECRETS_SOURCE=keyvault" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "KEY_VAULT_GRAPH_CLIENT_SECRET_NAME is required when SECRETS_SOURCE=keyvault")
	}
}

func TestValidateAllowsMissingGraphCredentials(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.GraphTenantID = ""
	cfg.GraphClientID = ""
	cfg.GraphClientSecret = ""

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
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

func completeConfig() Config {
	return Config{
		SecretsSource:              SecretsSourceEnv,
		KeyVaultURL:                "",
		KeyVaultSecretNames:        KeyVaultSecretNames{HMACSecret: "hook-hmac-secret", ServiceBusConnectionString: "servicebus-conn-str", GraphClientSecret: "graph-client-secret"},
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
		ServiceBusConnectionString: testServiceBusConnectionString,
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
		GraphTenantID:              "tenant-id",
		GraphClientID:              "client-id",
		GraphClientSecret:          "graph-client-secret",
	}
}
