package main

import (
	"net/http"
	"strings"
)

// rateLimitMiddleware bounds the request rate per caller across the API surface
// — a coarse abuse/accidental-DoS cap (a runaway client, a tight retry loop).
// It is NOT per-endpoint fairness; it's a safety ceiling.
//
// Keying: an authenticated request is bucketed per SESSION (the cairn_session
// cookie value), so one user's many tabs share a budget without a DB lookup;
// an unauthenticated request is bucketed per client IP. Health/readiness probes
// and the webhook endpoints (which have their own stricter per-IP limiter) are
// exempt.
func rateLimitMiddleware(limiter *ipRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := "ip:" + clientIP(r)
		if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
			key = "s:" + c.Value
		}
		if !limiter.allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitExempt(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return true
	}
	// Webhooks carry their own per-IP limiter and must accept provider bursts.
	return strings.HasPrefix(path, "/webhooks/")
}
