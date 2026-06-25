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

type Config struct {
	HTTPAddr           string
	HMACSecret         string
	EntraPrimaryDomain string
	ProblemBaseURL     string
	HMACClockSkew      time.Duration
	NonceTTL           time.Duration
	PortalAllowedCIDRs []string
	RateLimitPerIP     int
	RateLimitWindow    time.Duration
}

func Load() Config {
	return Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		HMACSecret:         os.Getenv("HOOK_HMAC_SECRET"),
		EntraPrimaryDomain: env("ENTRA_PRIMARY_DOMAIN", "nycu.edu.tw"),
		ProblemBaseURL:     strings.TrimRight(env("PROBLEM_BASE_URL", "https://nycu.edu.tw/problems"), "/"),
		HMACClockSkew:      30 * time.Second,
		NonceTTL:           60 * time.Second,
		PortalAllowedCIDRs: csvEnv("PORTAL_ALLOWED_CIDRS"),
		RateLimitPerIP:     intEnv("RATE_LIMIT_PER_IP", 500),
		RateLimitWindow:    time.Second,
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
	case c.RateLimitPerIP < 0:
		return errors.New("RateLimitPerIP must not be negative")
	case c.RateLimitWindow < 0:
		return errors.New("RateLimitWindow must not be negative")
	default:
		return validateCIDRs(c.PortalAllowedCIDRs)
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
