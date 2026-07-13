package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
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
	PasswordEncryptionKeyB64      string
	PasswordEncryptionKeyID       string
	GraphTenantID                 string
	GraphClientID                 string
	GraphClientSecret             string
	ObservabilityExporter         string
	OTLPExporterEndpoint          string
	AzureMonitorMetricResourceID  string
	AzureMonitorMetricRegion      string
	AzureMonitorMetricNamespace   string
	directClientModeErr           error
}

func Load() Config {
	directClientMode, directClientModeErr := boolEnv("DIRECT_CLIENT_MODE")
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
		PasswordMessageTTL:            300 * time.Second,
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
		return validateCIDRs("TRUSTED_PROXY_CIDRS", c.TrustedProxyCIDRs)
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
