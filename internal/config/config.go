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
	RateLimitPerIP                int
	RateLimitWindow               time.Duration
	ServiceBusConnectionString    string
	ServiceBusQueueName           string
	ServiceBusDeadLetterQueueName string
	PasswordMessageTTL            time.Duration
	PasswordEncryptionKeyB64      string
	PasswordEncryptionKeyID       string
	GraphTenantID                 string
	GraphClientID                 string
	GraphClientSecret             string
}

func Load() Config {
	return Config{
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
		RateLimitPerIP:                intEnv("RATE_LIMIT_PER_IP", 500),
		RateLimitWindow:               time.Second,
		ServiceBusConnectionString:    strings.TrimSpace(os.Getenv("SERVICEBUS_CONNECTION_STRING")),
		ServiceBusQueueName:           env("SERVICEBUS_QUEUE_NAME", "password-sync"),
		ServiceBusDeadLetterQueueName: env("SERVICEBUS_DEADLETTER_QUEUE_NAME", "password-sync-dlq"),
		PasswordMessageTTL:            300 * time.Second,
		PasswordEncryptionKeyB64:      strings.TrimSpace(os.Getenv("PASSWORD_ENCRYPTION_KEY_B64")),
		PasswordEncryptionKeyID:       env("PASSWORD_ENCRYPTION_KEY_ID", "password-payload-key-v1"),
		GraphTenantID:                 strings.TrimSpace(os.Getenv("GRAPH_TENANT_ID")),
		GraphClientID:                 strings.TrimSpace(os.Getenv("GRAPH_CLIENT_ID")),
		GraphClientSecret:             strings.TrimSpace(os.Getenv("GRAPH_CLIENT_SECRET")),
	}
}

func (c Config) Validate() error {
	if err := c.ValidateSecretLoadingInputs(); err != nil {
		return err
	}
	if err := c.ValidateHTTP(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(c.ServiceBusConnectionString) == "":
		return errors.New("SERVICEBUS_CONNECTION_STRING is required")
	case strings.TrimSpace(c.ServiceBusQueueName) == "":
		return errors.New("SERVICEBUS_QUEUE_NAME is required")
	case strings.TrimSpace(c.ServiceBusDeadLetterQueueName) == "":
		return errors.New("SERVICEBUS_DEADLETTER_QUEUE_NAME is required")
	case c.PasswordMessageTTL <= 0:
		return errors.New("PasswordMessageTTL must be positive")
	case strings.TrimSpace(c.PasswordEncryptionKeyB64) == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_B64 is required")
	case strings.TrimSpace(c.PasswordEncryptionKeyID) == "":
		return errors.New("PASSWORD_ENCRYPTION_KEY_ID is required")
	default:
		return nil
	}
}

func (c Config) ValidateHTTP() error {
	switch {
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
	case c.RateLimitPerIP < 0:
		return errors.New("RateLimitPerIP must not be negative")
	case c.RateLimitWindow < 0:
		return errors.New("RateLimitWindow must not be negative")
	default:
		return validateCIDRs(c.PortalAllowedCIDRs)
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
		case strings.TrimSpace(c.KeyVaultSecretNames.ServiceBusConnectionString) == "":
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

func validateCIDRs(values []string) error {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("PORTAL_ALLOWED_CIDRS contains invalid CIDR %q", value)
		}
	}
	return nil
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
