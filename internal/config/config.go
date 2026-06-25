package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr           string
	HMACSecret         string
	EntraPrimaryDomain string
	ProblemBaseURL     string
	HMACClockSkew      time.Duration
	NonceTTL           time.Duration
}

func Load() Config {
	return Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		HMACSecret:         os.Getenv("HOOK_HMAC_SECRET"),
		EntraPrimaryDomain: env("ENTRA_PRIMARY_DOMAIN", "nycu.edu.tw"),
		ProblemBaseURL:     strings.TrimRight(env("PROBLEM_BASE_URL", "https://nycu.edu.tw/problems"), "/"),
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
	}
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.HTTPAddr) == "":
		return errors.New("HTTP_ADDR is required")
	case strings.TrimSpace(c.HMACSecret) == "":
		return errors.New("HOOK_HMAC_SECRET is required")
	case strings.TrimSpace(c.EntraPrimaryDomain) == "":
		return errors.New("ENTRA_PRIMARY_DOMAIN is required")
	case strings.Contains(c.EntraPrimaryDomain, "@"):
		return fmt.Errorf("ENTRA_PRIMARY_DOMAIN must be a domain, got %q", c.EntraPrimaryDomain)
	case !strings.HasPrefix(c.ProblemBaseURL, "https://"):
		return errors.New("PROBLEM_BASE_URL must start with https://")
	case c.HMACClockSkew <= 0:
		return errors.New("HMACClockSkew must be positive")
	case c.NonceTTL <= 0:
		return errors.New("NonceTTL must be positive")
	default:
		return nil
	}
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
