package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type RateLimitConfig struct {
	AllowedCIDRs []string
	LimitPerIP   int
	Window       time.Duration
	ProblemBase  string
	Logger       *slog.Logger
	Recorder     observability.Recorder
}

type RateLimiter struct {
	allowedCIDRs []*net.IPNet
	limitPerIP   int
	window       time.Duration
	problemBase  string
	logger       *slog.Logger
	recorder     observability.Recorder
	mu           sync.Mutex
	counts       map[string]rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	if cfg.LimitPerIP <= 0 {
		cfg.LimitPerIP = 500
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Recorder == nil {
		cfg.Recorder = observability.NoopRecorder{}
	}

	return &RateLimiter{
		allowedCIDRs: parseCIDRs(cfg.AllowedCIDRs),
		limitPerIP:   cfg.LimitPerIP,
		window:       cfg.Window,
		problemBase:  cfg.ProblemBase,
		logger:       cfg.Logger,
		recorder:     cfg.Recorder,
		counts:       map[string]rateWindow{},
	}
}

func (l *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceIP := remoteIP(r)
		if len(l.allowedCIDRs) > 0 && !containsIP(l.allowedCIDRs, sourceIP) {
			recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusUnauthorized, "unauthorized", "source_ip_not_allowed")
			problem.Write(w, problem.Unauthorized(l.problemBase, r.URL.Path, requestid.From(r.Context()), "source ip is not allowed"))
			return
		}

		clientIP := sourceIP
		if len(l.allowedCIDRs) > 0 && containsIP(l.allowedCIDRs, sourceIP) {
			clientIP = forwardedClientIP(r, sourceIP)
		}
		if !l.allow(clientIP.String(), time.Now()) {
			recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusTooManyRequests, "rate_limited", "request_rate_exceeded")
			problem.Write(w, problem.TooManyRequests(l.problemBase, r.URL.Path, requestid.From(r.Context()), "request rate exceeded"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	current := l.counts[key]
	if current.start.IsZero() || now.Sub(current.start) >= l.window {
		l.counts[key] = rateWindow{start: now, count: 1}
		return true
	}
	if current.count >= l.limitPerIP {
		return false
	}
	current.count++
	l.counts[key] = current
	return true
}

func parseCIDRs(values []string) []*net.IPNet {
	cidrs := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, cidr, err := net.ParseCIDR(strings.TrimSpace(value))
		if err == nil {
			cidrs = append(cidrs, cidr)
		}
	}
	return cidrs
}

func containsIP(cidrs []*net.IPNet, ip net.IP) bool {
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return net.IPv4zero
	}
	return ip
}

func forwardedClientIP(r *http.Request, fallback net.IP) net.IP {
	raw := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if raw == "" {
		return fallback
	}
	first, _, _ := strings.Cut(raw, ",")
	ip := net.ParseIP(strings.TrimSpace(first))
	if ip == nil {
		return fallback
	}
	return ip
}
