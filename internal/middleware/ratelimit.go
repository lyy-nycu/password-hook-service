package middleware

import "net/http"

type RateLimiter struct{}

func NewRateLimiter() RateLimiter {
	return RateLimiter{}
}

func (l RateLimiter) Wrap(next http.Handler) http.Handler {
	return next
}
