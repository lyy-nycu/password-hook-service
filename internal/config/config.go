package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SecretsSourceEnv      = "env"
	SecretsSourceKeyVault = "keyvault"

	ServiceBusAuthConnectionString = "connection_string"
	ServiceBusAuthManagedIdentity  = "managed_identity"
)

const (
	ObservabilityExporterNone         = "none"
	ObservabilityExporterAzureMonitor = "azure_monitor"

	SyncStatusStoreMemory = "memory"
	SyncStatusStoreRedis  = "redis"
)

type KeyVaultSecretNames struct {
	HMACSecret                 string
	ServiceBusConnectionString string
	GraphClientSecret          string
	PasswordEncryptionKey      string
}

type Config struct {
	SecretsSource                 string
	KeyVaultURL                   string
	KeyVaultSecretNames           KeyVaultSecretNames
	HTTPAddr                      string
	HMACSecret                    string
	EntraPrimaryDomain            string
	EntraFallbackDomain           string
	ProblemBaseURL                string
	HMACClockSkew                 time.Duration
	NonceTTL                      time.Duration
	PortalAllowedCIDRs            []string
	TrustedProxyCIDRs             []string
	DirectClientMode              bool
	RateLimitPerIP                int
	RateLimitWindow               time.Duration
	HookMaxBodyBytes              int64
	ServiceBusAuthMode            string
	ServiceBusNamespaceFQDN       string
	ServiceBusConnectionString    string
	ServiceBusQueueName           string
	ServiceBusDeadLetterQueueName string
	PasswordMessageTTL            time.Duration
	SyncStatusStore               string
	RedisHost                     string
	RedisPort                     int
	RedisKeyPrefix                string
	SyncStatusTerminalTTL         time.Duration
	AzureClientID                 string
	PasswordEncryptionKeyB64      string
	PasswordEncryptionKeyID       string
	GraphTenantID                 string
	GraphClientID                 string
	GraphClientSecret             string
	ObservabilityExporter         string
	// OTLPExporterEndpoint holds the OTEL_EXPORTER_OTLP_ENDPOINT value the
	// ACA managed OpenTelemetry agent injects at runtime. It must never be
	// set explicitly by Terraform or hand-authored deployment configuration
	// (see deploy/terraform/modules/aca): the managed agent injects the
	// endpoint automatically, and setting it explicitly would compete with
	// or invalidate that injection.
	OTLPExporterEndpoint         string
	AzureMonitorMetricResourceID string
	AzureMonitorMetricRegion     string
	AzureMonitorMetricNamespace  string
	directClientModeErr          error
	redisPortErr                 error
	passwordMessageTTLErr        error
	syncStatusTerminalTTLErr     error
}

func Load() Config {
	directClientMode, directClientModeErr := boolEnv("DIRECT_CLIENT_MODE")
	redisPort, redisPortErr := strictIntEnv("REDIS_PORT")
	passwordMessageTTL, passwordMessageTTLErr := strictDurationEnv("PASSWORD_MESSAGE_TTL", 5*time.Minute)
	syncStatusTerminalTTL, syncStatusTerminalTTLErr := strictDurationEnv("SYNC_STATUS_TERMINAL_TTL", 90*24*time.Hour)
	cfg := Config{
		SecretsSource: strings.TrimSpace(os.Getenv("SECRETS_SOURCE")),
		KeyVaultURL:   strings.TrimSpace(os.Getenv("KEY_VAULT_URL")),
		KeyVaultSecretNames: KeyVaultSecretNames{
			HMACSecret:                 env("KEY_VAULT_HMAC_SECRET_NAME", "hook-hmac-secret"),
			ServiceBusConnectionString: env("KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME", "servicebus-conn-str"),
			GraphClientSecret:          env("KEY_VAULT_GRAPH_CLIENT_SECRET_NAME", "graph-client-secret"),
			PasswordEncryptionKey:      env("KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME", "password-payload-encryption-key"),
		},
		HTTPAddr:                      env("HTTP_ADDR", ":8080"),
		HMACSecret:                    os.Getenv("HOOK_HMAC_SECRET"),
		EntraPrimaryDomain:            env("ENTRA_PRIMARY_DOMAIN", "nycu.edu.tw"),
		EntraFallbackDomain:           strings.TrimSpace(os.Getenv("ENTRA_FALLBACK_DOMAIN")),
		ProblemBaseURL:                strings.TrimRight(env("PROBLEM_BASE_URL", "https://nycu.edu.tw/problems"), "/"),
		HMACClockSkew:                 30 * time.Second,
		NonceTTL:                      60 * time.Second,
		PortalAllowedCIDRs:            csvEnv("PORTAL_ALLOWED_CIDRS"),
		TrustedProxyCIDRs:             csvEnv("TRUSTED_PROXY_CIDRS"),
		DirectClientMode:              directClientMode,
		RateLimitPerIP:                intEnv("RATE_LIMIT_PER_IP", 500),
		RateLimitWindow:               durationEnv("RATE_LIMIT_WINDOW", time.Second),
		HookMaxBodyBytes:              int64Env("HOOK_MAX_BODY_BYTES", 64*1024),
		ServiceBusAuthMode:            env("SERVICEBUS_AUTH_MODE", ServiceBusAuthConnectionString),
		ServiceBusNamespaceFQDN:       strings.TrimSpace(os.Getenv("SERVICEBUS_NAMESPACE_FQDN")),
		ServiceBusConnectionString:    strings.TrimSpace(os.Getenv("SERVICEBUS_CONNECTION_STRING")),
		ServiceBusQueueName:           env("SERVICEBUS_QUEUE_NAME", "password-sync"),
		ServiceBusDeadLetterQueueName: env("SERVICEBUS_DEADLETTER_QUEUE_NAME", "password-sync-dlq"),
		PasswordMessageTTL:            passwordMessageTTL,
		SyncStatusStore:               strings.TrimSpace(os.Getenv("SYNC_STATUS_STORE")),
		RedisHost:                     strings.TrimSpace(os.Getenv("REDIS_HOST")),
		RedisPort:                     redisPort,
		RedisKeyPrefix:                env("REDIS_KEY_PREFIX", "password-hook:sync-status:"),
		SyncStatusTerminalTTL:         syncStatusTerminalTTL,
		AzureClientID:                 strings.TrimSpace(os.Getenv("AZURE_CLIENT_ID")),
		PasswordEncryptionKeyB64:      strings.TrimSpace(os.Getenv("PASSWORD_ENCRYPTION_KEY_B64")),
		PasswordEncryptionKeyID:       env("PASSWORD_ENCRYPTION_KEY_ID", "password-payload-key-v1"),
		GraphTenantID:                 strings.TrimSpace(os.Getenv("GRAPH_TENANT_ID")),
		GraphClientID:                 strings.TrimSpace(os.Getenv("GRAPH_CLIENT_ID")),
		GraphClientSecret:             strings.TrimSpace(os.Getenv("GRAPH_CLIENT_SECRET")),
		ObservabilityExporter:         env("OBSERVABILITY_EXPORTER", ObservabilityExporterNone),
		OTLPExporterEndpoint:          strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		AzureMonitorMetricResourceID:  strings.TrimSpace(os.Getenv("AZURE_MONITOR_METRIC_RESOURCE_ID")),
		AzureMonitorMetricRegion:      strings.TrimSpace(os.Getenv("AZURE_MONITOR_METRIC_REGION")),
		AzureMonitorMetricNamespace:   env("AZURE_MONITOR_METRIC_NAMESPACE", "password-hook-service"),
	}
	cfg.directClientModeErr = directClientModeErr
	cfg.redisPortErr = redisPortErr
	cfg.passwordMessageTTLErr = passwordMessageTTLErr
	cfg.syncStatusTerminalTTLErr = syncStatusTerminalTTLErr
	return cfg
}

func (c Config) Validate() error {
	if err := c.ValidateSecretLoadingInputs(); err != nil {
		return err
	}
	if err := c.ValidateHTTP(); err != nil {
		return err
	}
	if err := c.validateServiceBus(); err != nil {
		return err
	}
	if err := c.validateSyncStatus(); err != nil {
		return err
	}
	if err := c.validateObservability(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(c.PasswordEncryptionKeyB64) == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_B64 is required")
	case strings.TrimSpace(c.PasswordEncryptionKeyID) == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_ID is required")
	case strings.TrimSpace(c.GraphTenantID) == "":
		return errors.New("GRAPH_TENANT_ID is required")
	case strings.TrimSpace(c.GraphClientID) == "":
		return errors.New("GRAPH_CLIENT_ID is required")
	case strings.TrimSpace(c.GraphClientSecret) == "":
		return errors.New("GRAPH_CLIENT_SECRET is required")
	default:
		return nil
	}
}

func (c Config) validateServiceBus() error {
	if c.passwordMessageTTLErr != nil {
		return c.passwordMessageTTLErr
	}
	switch c.ServiceBusAuthMode {
	case "", ServiceBusAuthConnectionString:
		if strings.TrimSpace(c.ServiceBusConnectionString) == "" {
			return errors.New("SERVICEBUS_CONNECTION_STRING is required")
		}
	case ServiceBusAuthManagedIdentity:
		namespaceFQDN := strings.ToLower(strings.TrimSpace(c.ServiceBusNamespaceFQDN))
		if namespaceFQDN == "" {
			return errors.New("SERVICEBUS_NAMESPACE_FQDN is required when SERVICEBUS_AUTH_MODE=managed_identity")
		}
		if strings.Contains(namespaceFQDN, "://") || !strings.HasSuffix(namespaceFQDN, ".servicebus.windows.net") {
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

func (c Config) validateSyncStatus() error {
	if c.redisPortErr != nil {
		return c.redisPortErr
	}
	if c.syncStatusTerminalTTLErr != nil {
		return c.syncStatusTerminalTTLErr
	}
	switch c.SyncStatusStore {
	case SyncStatusStoreMemory:
		return nil
	case SyncStatusStoreRedis:
		switch {
		case strings.TrimSpace(c.RedisHost) == "":
			return errors.New("REDIS_HOST is required when SYNC_STATUS_STORE=redis")
		case strings.Contains(c.RedisHost, "://") || strings.ContainsAny(c.RedisHost, "/@:"):
			return errors.New("REDIS_HOST must be a host name without a scheme, path, or port")
		case c.RedisPort <= 0 || c.RedisPort > 65535:
			return errors.New("REDIS_PORT must be between 1 and 65535 when SYNC_STATUS_STORE=redis")
		case strings.TrimSpace(c.RedisKeyPrefix) == "":
			return errors.New("REDIS_KEY_PREFIX is required when SYNC_STATUS_STORE=redis")
		case c.PasswordMessageTTL < time.Millisecond:
			return errors.New("PASSWORD_MESSAGE_TTL must be at least 1ms when SYNC_STATUS_STORE=redis")
		case c.SyncStatusTerminalTTL < time.Millisecond:
			return errors.New("SYNC_STATUS_TERMINAL_TTL must be at least 1ms when SYNC_STATUS_STORE=redis")
		case strings.TrimSpace(c.AzureClientID) == "":
			return errors.New("AZURE_CLIENT_ID is required when SYNC_STATUS_STORE=redis")
		case uuid.Validate(c.AzureClientID) != nil:
			return errors.New("AZURE_CLIENT_ID must be a valid UUID when SYNC_STATUS_STORE=redis")
		default:
			return nil
		}
	default:
		return errors.New("SYNC_STATUS_STORE must be memory or redis")
	}
}

func (c Config) validateObservability() error {
	switch c.ObservabilityExporter {
	case "", ObservabilityExporterNone:
		return nil
	case ObservabilityExporterAzureMonitor:
		if strings.TrimSpace(c.OTLPExporterEndpoint) == "" {
			return errors.New("OTEL_EXPORTER_OTLP_ENDPOINT is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricResourceID) == "" {
			return errors.New("AZURE_MONITOR_METRIC_RESOURCE_ID is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricRegion) == "" {
			return errors.New("AZURE_MONITOR_METRIC_REGION is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricNamespace) == "" {
			return errors.New("AZURE_MONITOR_METRIC_NAMESPACE is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		return nil
	default:
		return errors.New("OBSERVABILITY_EXPORTER must be none or azure_monitor")
	}
}

func (c Config) ValidateHTTP() error {
	switch {
	case c.directClientModeErr != nil:
		return c.directClientModeErr
	case strings.TrimSpace(c.HTTPAddr) == "":
		return errors.New("HTTP_ADDR is required")
	case strings.TrimSpace(c.HMACSecret) == "":
		return errors.New("HOOK_HMAC_SECRET is required")
	case strings.TrimSpace(c.EntraPrimaryDomain) == "":
		return errors.New("ENTRA_PRIMARY_DOMAIN is required")
	case strings.Contains(c.EntraPrimaryDomain, "@"):
		return fmt.Errorf("ENTRA_PRIMARY_DOMAIN must be a domain, got %q", c.EntraPrimaryDomain)
	case strings.Contains(c.EntraFallbackDomain, "@"):
		return fmt.Errorf("ENTRA_FALLBACK_DOMAIN must be a domain, got %q", c.EntraFallbackDomain)
	case !strings.HasPrefix(c.ProblemBaseURL, "https://"):
		return errors.New("PROBLEM_BASE_URL must start with https://")
	case c.HMACClockSkew <= 0:
		return errors.New("HMACClockSkew must be positive")
	case c.NonceTTL <= 0:
		return errors.New("NonceTTL must be positive")
	case !hasNonBlank(c.PortalAllowedCIDRs):
		return errors.New("PORTAL_ALLOWED_CIDRS is required")
	case c.DirectClientMode && hasNonBlank(c.TrustedProxyCIDRs):
		return errors.New("TRUSTED_PROXY_CIDRS must be empty when DIRECT_CLIENT_MODE=true")
	case !c.DirectClientMode && !hasNonBlank(c.TrustedProxyCIDRs):
		return errors.New("TRUSTED_PROXY_CIDRS is required when DIRECT_CLIENT_MODE=false")
	case c.RateLimitPerIP <= 0:
		return errors.New("RateLimitPerIP must be positive")
	case c.RateLimitWindow <= 0:
		return errors.New("RateLimitWindow must be positive")
	case c.HookMaxBodyBytes <= 0:
		return errors.New("HookMaxBodyBytes must be positive")
	default:
		if err := validateCIDRs("PORTAL_ALLOWED_CIDRS", c.PortalAllowedCIDRs); err != nil {
			return err
		}
		if err := validateCIDRs("TRUSTED_PROXY_CIDRS", c.TrustedProxyCIDRs); err != nil {
			return err
		}
		if err := rejectUnrestrictedCIDRs("TRUSTED_PROXY_CIDRS", c.TrustedProxyCIDRs); err != nil {
			return err
		}
		return rejectOverlappingCIDRs(c.TrustedProxyCIDRs, c.PortalAllowedCIDRs)
	}
}

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
		case c.ServiceBusAuthMode != ServiceBusAuthManagedIdentity && strings.TrimSpace(c.KeyVaultSecretNames.ServiceBusConnectionString) == "":
			return errors.New("KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME is required when SECRETS_SOURCE=keyvault")
		case strings.TrimSpace(c.KeyVaultSecretNames.GraphClientSecret) == "":
			return errors.New("KEY_VAULT_GRAPH_CLIENT_SECRET_NAME is required when SECRETS_SOURCE=keyvault")
		case strings.TrimSpace(c.KeyVaultSecretNames.PasswordEncryptionKey) == "":
			return errors.New("KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME is required when SECRETS_SOURCE=keyvault")
		default:
			return nil
		}
	default:
		return errors.New("SECRETS_SOURCE must be env or keyvault")
	}
}

func hasNonBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func validateCIDRs(envName string, values []string) error {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("%s contains invalid CIDR %q", envName, value)
		}
	}
	return nil
}

// rejectOverlappingCIDRs rejects any trusted-proxy CIDR that overlaps a
// portal-allowed CIDR. The trusted-proxy set identifies immediate proxy
// peers, never portal clients; an overlap would let a direct portal peer
// be treated as a trusted proxy and forge X-Forwarded-For.
func rejectOverlappingCIDRs(trusted, portal []string) error {
	for _, t := range trusted {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		_, trustedNet, err := net.ParseCIDR(t)
		if err != nil {
			return fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid CIDR %q", t)
		}
		for _, p := range portal {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			_, portalNet, err := net.ParseCIDR(p)
			if err != nil {
				return fmt.Errorf("PORTAL_ALLOWED_CIDRS contains invalid CIDR %q", p)
			}
			if trustedNet.Contains(portalNet.IP) || portalNet.Contains(trustedNet.IP) {
				return fmt.Errorf("TRUSTED_PROXY_CIDRS %q must not overlap PORTAL_ALLOWED_CIDRS %q", t, p)
			}
		}
	}
	return nil
}

// rejectUnrestrictedCIDRs rejects a trusted-proxy CIDR that matches every
// address (e.g. 0.0.0.0/0 or ::/0), which would let any peer spoof
// X-Forwarded-For and defeat the trust boundary.
func rejectUnrestrictedCIDRs(envName string, values []string) error {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("%s contains invalid CIDR %q", envName, value)
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return fmt.Errorf("%s must not contain unrestricted CIDR %q", envName, value)
		}
	}
	return nil
}

func boolEnv(key string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func strictIntEnv(key string) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}

func strictDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", key)
	}
	return value, nil
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func csvEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	values := strings.Split(raw, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func int64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}
