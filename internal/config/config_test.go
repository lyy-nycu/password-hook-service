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

func TestLoadSyncStatusDefaults(t *testing.T) {
	t.Parallel()

	cfg := Load()

	if cfg.RedisKeyPrefix != "password-hook:sync-status:" {
		t.Fatalf("RedisKeyPrefix = %q", cfg.RedisKeyPrefix)
	}
	if cfg.SyncStatusTerminalTTL != 90*24*time.Hour {
		t.Fatalf("SyncStatusTerminalTTL = %s, want 90d", cfg.SyncStatusTerminalTTL)
	}
	if cfg.PasswordMessageTTL != 5*time.Minute {
		t.Fatalf("PasswordMessageTTL = %s, want 5m", cfg.PasswordMessageTTL)
	}
}

func TestLoadRedisSyncStatusConfig(t *testing.T) {
	t.Setenv("SYNC_STATUS_STORE", " redis ")
	t.Setenv("REDIS_HOST", " cache.example.redis.azure.net ")
	t.Setenv("REDIS_PORT", "10000")
	t.Setenv("REDIS_KEY_PREFIX", "deployment:status:")
	t.Setenv("SYNC_STATUS_TERMINAL_TTL", "2160h")
	t.Setenv("PASSWORD_MESSAGE_TTL", "4m")
	t.Setenv("AZURE_CLIENT_ID", " 00000000-0000-0000-0000-000000000003 ")

	cfg := Load()

	if cfg.SyncStatusStore != SyncStatusStoreRedis || cfg.RedisHost != "cache.example.redis.azure.net" || cfg.RedisPort != 10000 {
		t.Fatalf("Redis config = %#v", cfg)
	}
	if cfg.RedisKeyPrefix != "deployment:status:" || cfg.SyncStatusTerminalTTL != 90*24*time.Hour {
		t.Fatalf("Redis retention config = %#v", cfg)
	}
	if cfg.PasswordMessageTTL != 4*time.Minute || cfg.AzureClientID != "00000000-0000-0000-0000-000000000003" {
		t.Fatalf("Redis identity/message TTL config = %#v", cfg)
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

func TestValidateHTTPRejectsUnrestrictedTrustedProxyCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cidr string
		want string
	}{
		{name: "ipv4 unrestricted", cidr: "0.0.0.0/0", want: `TRUSTED_PROXY_CIDRS must not contain unrestricted CIDR "0.0.0.0/0"`},
		{name: "ipv6 unrestricted", cidr: "::/0", want: `TRUSTED_PROXY_CIDRS must not contain unrestricted CIDR "::/0"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := completeConfig()
			cfg.DirectClientMode = false
			cfg.TrustedProxyCIDRs = []string{tt.cidr}
			if err := cfg.ValidateHTTP(); err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateHTTP error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateHTTPRejectsTrustedProxyCIDROverlappingPortalCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		portal  []string
		trusted []string
		want    string
	}{
		{
			name:    "identical CIDR",
			portal:  []string{"192.0.2.0/24"},
			trusted: []string{"192.0.2.0/24"},
			want:    `TRUSTED_PROXY_CIDRS "192.0.2.0/24" must not overlap PORTAL_ALLOWED_CIDRS "192.0.2.0/24"`,
		},
		{
			name:    "trusted is subset of portal",
			portal:  []string{"192.0.2.0/24"},
			trusted: []string{"192.0.2.128/25"},
			want:    `TRUSTED_PROXY_CIDRS "192.0.2.128/25" must not overlap PORTAL_ALLOWED_CIDRS "192.0.2.0/24"`,
		},
		{
			name:    "portal is subset of trusted",
			portal:  []string{"192.0.2.128/25"},
			trusted: []string{"192.0.2.0/24"},
			want:    `TRUSTED_PROXY_CIDRS "192.0.2.0/24" must not overlap PORTAL_ALLOWED_CIDRS "192.0.2.128/25"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := completeConfig()
			cfg.DirectClientMode = false
			cfg.PortalAllowedCIDRs = tt.portal
			cfg.TrustedProxyCIDRs = tt.trusted
			if err := cfg.ValidateHTTP(); err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateHTTP error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateHTTPAcceptsNonOverlappingPortalAndTrustedProxyCIDRs(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.DirectClientMode = false
	cfg.PortalAllowedCIDRs = []string{"192.0.2.0/24"}
	cfg.TrustedProxyCIDRs = []string{"10.0.8.0/26"}
	if err := cfg.ValidateHTTP(); err != nil {
		t.Fatalf("ValidateHTTP returned error: %v", err)
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

func TestLoadRejectsMalformedSyncStatusNumbers(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		validate func(Config) error
		want     string
	}{
		{name: "redis port", key: "REDIS_PORT", value: "tls", validate: Config.validateSyncStatus, want: "REDIS_PORT must be an integer"},
		{name: "terminal TTL", key: "SYNC_STATUS_TERMINAL_TTL", value: "ninety-days", validate: Config.validateSyncStatus, want: "SYNC_STATUS_TERMINAL_TTL must be a valid duration"},
		{name: "message TTL", key: "PASSWORD_MESSAGE_TTL", value: "five-minutes", validate: Config.validateServiceBus, want: "PASSWORD_MESSAGE_TTL must be a valid duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			cfg := Load()
			if err := tt.validate(cfg); err == nil || err.Error() != tt.want {
				t.Fatalf("validation error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateSyncStatusStoreModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "missing mode", edit: func(cfg *Config) { cfg.SyncStatusStore = "" }, want: "SYNC_STATUS_STORE must be memory or redis"},
		{name: "unknown mode", edit: func(cfg *Config) { cfg.SyncStatusStore = "disk" }, want: "SYNC_STATUS_STORE must be memory or redis"},
		{name: "missing host", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.RedisHost = "" }, want: "REDIS_HOST is required when SYNC_STATUS_STORE=redis"},
		{name: "missing port", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.RedisPort = 0 }, want: "REDIS_PORT must be between 1 and 65535 when SYNC_STATUS_STORE=redis"},
		{name: "invalid port", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.RedisPort = 65536 }, want: "REDIS_PORT must be between 1 and 65535 when SYNC_STATUS_STORE=redis"},
		{name: "missing prefix", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.RedisKeyPrefix = "" }, want: "REDIS_KEY_PREFIX is required when SYNC_STATUS_STORE=redis"},
		{name: "host includes port", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.RedisHost = "cache.example:10000" }, want: "REDIS_HOST must be a host name without a scheme, path, or port"},
		{name: "sub-millisecond message TTL", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.PasswordMessageTTL = time.Nanosecond }, want: "PASSWORD_MESSAGE_TTL must be at least 1ms when SYNC_STATUS_STORE=redis"},
		{name: "invalid terminal TTL", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.SyncStatusTerminalTTL = 0 }, want: "SYNC_STATUS_TERMINAL_TTL must be at least 1ms when SYNC_STATUS_STORE=redis"},
		{name: "missing UAMI", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.AzureClientID = "" }, want: "AZURE_CLIENT_ID is required when SYNC_STATUS_STORE=redis"},
		{name: "invalid UAMI", edit: func(cfg *Config) { setCompleteRedisConfig(cfg); cfg.AzureClientID = "not-a-uuid" }, want: "AZURE_CLIENT_ID must be a valid UUID when SYNC_STATUS_STORE=redis"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := completeConfig()
			tt.edit(&cfg)
			if err := cfg.validateSyncStatus(); err == nil || err.Error() != tt.want {
				t.Fatalf("validateSyncStatus error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateSyncStatusStoreAcceptsMemoryWithoutRedisConfig(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.RedisHost = ""
	cfg.RedisPort = 0
	cfg.RedisKeyPrefix = ""
	cfg.SyncStatusTerminalTTL = 0
	cfg.AzureClientID = ""

	if err := cfg.validateSyncStatus(); err != nil {
		t.Fatalf("validateSyncStatus returned error: %v", err)
	}
}

func TestValidateSyncStatusStoreAcceptsRedis(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	setCompleteRedisConfig(&cfg)

	if err := cfg.validateSyncStatus(); err != nil {
		t.Fatalf("validateSyncStatus returned error: %v", err)
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
		SyncStatusStore:               SyncStatusStoreMemory,
		PasswordEncryptionKeyB64:      base64.StdEncoding.EncodeToString(make([]byte, 32)),
		PasswordEncryptionKeyID:       "password-payload-key-v1",
		GraphTenantID:                 "tenant-id",
		GraphClientID:                 "client-id",
		GraphClientSecret:             "graph-client-secret",
	}
}

func setCompleteRedisConfig(cfg *Config) {
	cfg.SyncStatusStore = SyncStatusStoreRedis
	cfg.RedisHost = "cache.example.redis.azure.net"
	cfg.RedisPort = 10000
	cfg.RedisKeyPrefix = "password-hook:sync-status:"
	cfg.SyncStatusTerminalTTL = 90 * 24 * time.Hour
	cfg.AzureClientID = "00000000-0000-0000-0000-000000000003"
}
