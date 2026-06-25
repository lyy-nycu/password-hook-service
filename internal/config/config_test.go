package config

import (
	"testing"
	"time"
)

func TestValidateRequiresHMACSecret(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTPAddr:           ":8080",
		EntraPrimaryDomain: "nycu.edu.tw",
		ProblemBaseURL:     "https://nycu.edu.tw/problems",
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error without HMAC secret")
	}
}

func TestValidateAcceptsCompleteConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTPAddr:           ":8080",
		HMACSecret:         "shared-secret",
		EntraPrimaryDomain: "nycu.edu.tw",
		ProblemBaseURL:     "https://nycu.edu.tw/problems",
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsInvalidPortalAllowedCIDR(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTPAddr:           ":8080",
		HMACSecret:         "shared-secret",
		EntraPrimaryDomain: "nycu.edu.tw",
		ProblemBaseURL:     "https://nycu.edu.tw/problems",
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
		PortalAllowedCIDRs: []string{"not-a-cidr"},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil error for invalid PORTAL_ALLOWED_CIDRS")
	}
}
