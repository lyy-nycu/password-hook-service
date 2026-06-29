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

func completeConfig() Config {
	return Config{
		HTTPAddr:                   ":8080",
		HMACSecret:                 "shared-secret",
		EntraPrimaryDomain:         "nycu.edu.tw",
		ProblemBaseURL:             "https://nycu.edu.tw/problems",
		HMACClockSkew:              30 * time.Second,
		NonceTTL:                   60 * time.Second,
		PortalAllowedCIDRs:         nil,
		RateLimitPerIP:             500,
		RateLimitWindow:            time.Second,
		ServiceBusConnectionString: testServiceBusConnectionString,
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
}
