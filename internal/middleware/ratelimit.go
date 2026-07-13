package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type RateLimitConfig struct {
	AllowedCIDRs      []string
	TrustedProxyCIDRs []string
	LimitPerIP        int
	Window            time.Duration
	ProblemBase       string
	Logger            *slog.Logger
	Recorder          observability.Recorder
}

type RateLimiter struct {
	allowedCIDRs      []*net.IPNet
	trustedProxyCIDRs []*net.IPNet
	limitPerIP        int
	window            time.Duration
	problemBase       string
	logger            *slog.Logger
	recorder          observability.Recorder
	mu                sync.Mutex
	counts            map[string]rateWindow
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
		allowedCIDRs:      parseCIDRs(cfg.AllowedCIDRs),
		trustedProxyCIDRs: parseCIDRs(cfg.TrustedProxyCIDRs),
		limitPerIP:        cfg.LimitPerIP,
		window:            cfg.Window,
		problemBase:       cfg.ProblemBase,
		logger:            cfg.Logger,
		recorder:          cfg.Recorder,
		counts:            map[string]rateWindow{},
	}
}

func (l *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceIP := l.sourceIP(r)
		if !l.sourceAllowed(sourceIP) {
			recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusUnauthorized, "unauthorized", "source_ip_not_allowed")
			problem.Write(w, problem.Unauthorized(l.problemBase, r.URL.Path, requestid.From(r.Context()), "source ip is not allowed"))
			return
		}

		key := l.rateKey(sourceIP)
		if !l.allow(key, time.Now()) {
			recordMiddlewareOutcome(r.Context(), l.logger, l.recorder, requestid.From(r.Context()), "ratelimit", http.StatusTooManyRequests, "rate_limited", "request_rate_exceeded")
			problem.Write(w, problem.TooManyRequests(l.problemBase, r.URL.Path, requestid.From(r.Context()), "request rate exceeded"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) sourceAllowed(sourceIP net.IP) bool {
	return len(l.allowedCIDRs) > 0 && containsIP(l.allowedCIDRs, sourceIP)
}

// sourceIP resolves a forwarded caller only across an explicitly trusted proxy
// boundary. An empty trusted-proxy set is direct-client mode.
func (l *RateLimiter) sourceIP(r *http.Request) net.IP {
	peer, ok := parseIPWithOptionalPort(r.RemoteAddr)
	if !ok {
		return nil
	}
	if len(l.trustedProxyCIDRs) == 0 || !containsIP(l.trustedProxyCIDRs, peer) {
		return peer
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return nil
	}
	hopValues := strings.Split(values[0], ",")
	hops := make([]net.IP, len(hopValues))
	for i, value := range hopValues {
		hop, valid := parseIPWithOptionalPort(value)
		if !valid {
			return nil
		}
		hops[i] = hop
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !containsIP(l.trustedProxyCIDRs, hops[i]) {
			return hops[i]
		}
	}
	return nil
}

func (l *RateLimiter) rateKey(sourceIP net.IP) string {
	return sourceIP.String()
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

func parseIPWithOptionalPort(value string) (net.IP, bool) {
	value = strings.TrimSpace(value)
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return net.IP(addr.Unmap().AsSlice()), true
	}

	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return nil, false
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return nil, false
	}
	return net.IP(addr.Unmap().AsSlice()), true
}
