package config

import (
	"testing"
	"time"
)

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
		ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=dGVzdA==",
		ServiceBusQueueName:        "password-sync",
		PasswordMessageTTL:         300 * time.Second,
	}
}
