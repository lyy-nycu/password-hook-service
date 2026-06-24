package config

import (
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

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
