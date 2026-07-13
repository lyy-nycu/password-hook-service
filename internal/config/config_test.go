package config

import (
	"encoding/base64"
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

func TestLoadAzureMonitorExporterConfig(t *testing.T) {
	t.Setenv("OBSERVABILITY_EXPORTER", " azure_monitor ")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("AZURE_MONITOR_METRIC_RESOURCE_ID", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook")
	t.Setenv("AZURE_MONITOR_METRIC_REGION", "eastasia")
	t.Setenv("AZURE_MONITOR_METRIC_NAMESPACE", "password-hook-service")

	cfg := Load()

	if cfg.ObservabilityExporter != ObservabilityExporterAzureMonitor {
		t.Fatalf("ObservabilityExporter = %q, want azure_monitor", cfg.ObservabilityExporter)
	}
	if cfg.OTLPExporterEndpoint != "http://localhost:4318" {
		t.Fatalf("OTLPExporterEndpoint = %q", cfg.OTLPExporterEndpoint)
	}
	if cfg.AzureMonitorMetricResourceID == "" || cfg.AzureMonitorMetricRegion != "eastasia" || cfg.AzureMonitorMetricNamespace != "password-hook-service" {
		t.Fatalf("Azure Monitor metric config = %#v", cfg)
	}
}

func TestValidateAzureMonitorExporterRequiresMetricConfig(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ObservabilityExporter = ObservabilityExporterAzureMonitor
	cfg.OTLPExporterEndpoint = "http://localhost:4318"
	cfg.AzureMonitorMetricResourceID = ""
	cfg.AzureMonitorMetricRegion = "eastasia"
	cfg.AzureMonitorMetricNamespace = "password-hook-service"

	err := cfg.Validate()
	if err == nil || err.Error() != "AZURE_MONITOR_METRIC_RESOURCE_ID is required when OBSERVABILITY_EXPORTER=azure_monitor" {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestLoadDefaultsPasswordEncryptionConfig(t *testing.T) {
	t.Parallel()

	cfg := Load()

	if cfg.KeyVaultSecretNames.PasswordEncryptionKey != "password-payload-encryption-key" {
		t.Fatalf("PasswordEncryptionKey secret name = %q", cfg.KeyVaultSecretNames.PasswordEncryptionKey)
	}
	if cfg.PasswordEncryptionKeyID != "password-payload-key-v1" {
		t.Fatalf("PasswordEncryptionKeyID = %q", cfg.PasswordEncryptionKeyID)
	}
	if cfg.ServiceBusDeadLetterQueueName != "password-sync-dlq" {
		t.Fatalf("ServiceBusDeadLetterQueueName = %q", cfg.ServiceBusDeadLetterQueueName)
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

func TestValidateHTTPRequiresPortalAllowedCIDRs(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PortalAllowedCIDRs = nil

	if err := cfg.ValidateHTTP(); err == nil || err.Error() != "PORTAL_ALLOWED_CIDRS is required" {
		t.Fatalf("ValidateHTTP error = %v, want PORTAL_ALLOWED_CIDRS is required", err)
	}
}

func TestValidateHTTPRequiresNonBlankPortalAllowedCIDRs(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PortalAllowedCIDRs = []string{" ", ""}

	if err := cfg.ValidateHTTP(); err == nil || err.Error() != "PORTAL_ALLOWED_CIDRS is required" {
		t.Fatalf("ValidateHTTP error = %v, want PORTAL_ALLOWED_CIDRS is required", err)
	}
}

func TestLoadAPIProtectionSettings(t *testing.T) {
	t.Setenv("PORTAL_ALLOWED_CIDRS", " 192.0.2.0/24, 2001:db8::/32 ")
	t.Setenv("TRUSTED_PROXY_CIDRS", " 10.0.0.0/24, 2001:db8:1::/64 ")
	t.Setenv("DIRECT_CLIENT_MODE", "false")
	t.Setenv("RATE_LIMIT_PER_IP", "750")
	t.Setenv("RATE_LIMIT_WINDOW", "2s")
	t.Setenv("HOOK_MAX_BODY_BYTES", "32768")

	cfg := Load()

	if got, want := cfg.PortalAllowedCIDRs, []string{"192.0.2.0/24", "2001:db8::/32"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PortalAllowedCIDRs = %#v, want %#v", got, want)
	}
	if got, want := cfg.TrustedProxyCIDRs, []string{"10.0.0.0/24", "2001:db8:1::/64"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("TrustedProxyCIDRs = %#v, want %#v", got, want)
	}
	if cfg.RateLimitPerIP != 750 {
		t.Fatalf("RateLimitPerIP = %d, want 750", cfg.RateLimitPerIP)
	}
	if cfg.RateLimitWindow != 2*time.Second {
		t.Fatalf("RateLimitWindow = %s, want 2s", cfg.RateLimitWindow)
	}
	if cfg.HookMaxBodyBytes != 32768 {
		t.Fatalf("HookMaxBodyBytes = %d, want 32768", cfg.HookMaxBodyBytes)
	}
}

func TestValidateHTTPRequiresExplicitClientAddressMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		direct       bool
		trustedCIDRs []string
		want         string
	}{
		{name: "neither mode", want: "TRUSTED_PROXY_CIDRS is required when DIRECT_CLIENT_MODE=false"},
		{name: "both modes", direct: true, trustedCIDRs: []string{"10.0.0.0/24"}, want: "TRUSTED_PROXY_CIDRS must be empty when DIRECT_CLIENT_MODE=true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := completeConfig()
			cfg.DirectClientMode = tt.direct
			cfg.TrustedProxyCIDRs = tt.trustedCIDRs
			if err := cfg.ValidateHTTP(); err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateHTTP error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateHTTPAcceptsTrustedProxyMode(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.DirectClientMode = false
	cfg.TrustedProxyCIDRs = []string{"10.0.0.0/24", "2001:db8:1::/64"}
	if err := cfg.ValidateHTTP(); err != nil {
		t.Fatalf("ValidateHTTP returned error: %v", err)
	}
}

func TestValidateHTTPRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.DirectClientMode = false
	cfg.TrustedProxyCIDRs = []string{"not-a-cidr"}
	if err := cfg.ValidateHTTP(); err == nil || err.Error() != `TRUSTED_PROXY_CIDRS contains invalid CIDR "not-a-cidr"` {
		t.Fatalf("ValidateHTTP error = %v", err)
	}
}

func TestLoadRejectsInvalidDirectClientMode(t *testing.T) {
	t.Setenv("DIRECT_CLIENT_MODE", "sometimes")
	cfg := Load()
	if err := cfg.ValidateHTTP(); err == nil || err.Error() != "DIRECT_CLIENT_MODE must be a boolean" {
		t.Fatalf("ValidateHTTP error = %v", err)
	}
}

func TestValidateHTTPRejectsInvalidAPIProtectionSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "zero rate limit", edit: func(cfg *Config) { cfg.RateLimitPerIP = 0 }, want: "RateLimitPerIP must be positive"},
		{name: "negative rate limit", edit: func(cfg *Config) { cfg.RateLimitPerIP = -1 }, want: "RateLimitPerIP must be positive"},
		{name: "zero rate window", edit: func(cfg *Config) { cfg.RateLimitWindow = 0 }, want: "RateLimitWindow must be positive"},
		{name: "negative rate window", edit: func(cfg *Config) { cfg.RateLimitWindow = -time.Second }, want: "RateLimitWindow must be positive"},
		{name: "zero max body", edit: func(cfg *Config) { cfg.HookMaxBodyBytes = 0 }, want: "HookMaxBodyBytes must be positive"},
		{name: "negative max body", edit: func(cfg *Config) { cfg.HookMaxBodyBytes = -1 }, want: "HookMaxBodyBytes must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := completeConfig()
			tt.edit(&cfg)

			err := cfg.ValidateHTTP()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateHTTP error = %v, want %q", err, tt.want)
			}
		})
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

func TestValidateRequiresPasswordEncryptionKeyB64(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PasswordEncryptionKeyB64 = ""

	if err := cfg.Validate(); err == nil || err.Error() != "PASSWORD_ENCRYPTION_KEY_B64 is required" {
		t.Fatalf("Validate error = %v, want PASSWORD_ENCRYPTION_KEY_B64 is required", err)
	}
}

func TestValidateRequiresPasswordEncryptionKeyID(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PasswordEncryptionKeyID = ""

	if err := cfg.Validate(); err == nil || err.Error() != "PASSWORD_ENCRYPTION_KEY_ID is required" {
		t.Fatalf("Validate error = %v, want PASSWORD_ENCRYPTION_KEY_ID is required", err)
	}
}

func TestValidateRequiresServiceBusDeadLetterQueueName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusDeadLetterQueueName = ""

	if err := cfg.Validate(); err == nil || err.Error() != "SERVICEBUS_DEADLETTER_QUEUE_NAME is required" {
		t.Fatalf("Validate error = %v, want SERVICEBUS_DEADLETTER_QUEUE_NAME is required", err)
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

func TestValidateKeyVaultSourceRequiresPasswordEncryptionKeyName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.SecretsSource = SecretsSourceKeyVault
	cfg.KeyVaultURL = "https://nycu-password-hook.vault.azure.net/"
	cfg.KeyVaultSecretNames.PasswordEncryptionKey = ""

	if err := cfg.ValidateSecretLoadingInputs(); err == nil || err.Error() != "KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME is required when SECRETS_SOURCE=keyvault" {
		t.Fatalf("ValidateSecretLoadingInputs error = %v, want %q", err, "KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME is required when SECRETS_SOURCE=keyvault")
	}
}

func TestValidateRequiresGraphCredentials(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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
	if cfg.KeyVaultSecretNames.PasswordEncryptionKey != "password-payload-encryption-key" {
		t.Fatalf("PasswordEncryptionKey name = %q", cfg.KeyVaultSecretNames.PasswordEncryptionKey)
	}
	if cfg.GraphTenantID != "tenant-id" || cfg.GraphClientID != "client-id" {
		t.Fatalf("Graph tenant/client = %q/%q", cfg.GraphTenantID, cfg.GraphClientID)
	}
}

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

func TestValidateManagedIdentityNormalizesNamespaceFQDN(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusAuthMode = ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "  NYCU-PASSWORD-HOOK.SERVICEBUS.WINDOWS.NET  "

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

func TestValidateRejectsInvalidServiceBusAuthMode(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusAuthMode = "sas"

	err := cfg.Validate()
	if err == nil || err.Error() != "SERVICEBUS_AUTH_MODE must be connection_string or managed_identity" {
		t.Fatalf("Validate error = %v", err)
	}
}

func completeConfig() Config {
	return Config{
		SecretsSource:                 SecretsSourceEnv,
		KeyVaultURL:                   "",
		KeyVaultSecretNames:           KeyVaultSecretNames{HMACSecret: "hook-hmac-secret", ServiceBusConnectionString: "servicebus-conn-str", GraphClientSecret: "graph-client-secret", PasswordEncryptionKey: "password-payload-encryption-key"},
		HTTPAddr:                      ":8080",
		HMACSecret:                    "shared-secret",
		EntraPrimaryDomain:            "nycu.edu.tw",
		EntraFallbackDomain:           "nycu.onmicrosoft.com",
		ProblemBaseURL:                "https://nycu.edu.tw/problems",
		HMACClockSkew:                 30 * time.Second,
		NonceTTL:                      60 * time.Second,
		PortalAllowedCIDRs:            []string{"192.0.2.0/24"},
		DirectClientMode:              true,
		RateLimitPerIP:                500,
		RateLimitWindow:               time.Second,
		HookMaxBodyBytes:              64 * 1024,
		ServiceBusAuthMode:            ServiceBusAuthConnectionString,
		ServiceBusNamespaceFQDN:       "",
		ServiceBusConnectionString:    testServiceBusConnectionString,
		ServiceBusQueueName:           "password-sync",
		ServiceBusDeadLetterQueueName: "password-sync-dlq",
		PasswordMessageTTL:            300 * time.Second,
		PasswordEncryptionKeyB64:      base64.StdEncoding.EncodeToString(make([]byte, 32)),
		PasswordEncryptionKeyID:       "password-payload-key-v1",
		GraphTenantID:                 "tenant-id",
		GraphClientID:                 "client-id",
		GraphClientSecret:             "graph-client-secret",
	}
}
