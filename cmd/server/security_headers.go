package main

import "net/http"

// securityHeaders wraps the mux with baseline hardening headers applied to
// every response. These are cheap, broadly-compatible defenses; the reverse
// proxy (Caddy) may set additional ones in front.
//
// Deliberately omitted: a strict Content-Security-Policy. The SPA bundle and
// MapLibre/uPlot need a tailored policy, and a wrong CSP silently breaks the
// app — that belongs in the proxy config where it can be tuned per deploy.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Don't let browsers MIME-sniff responses into a different type.
		h.Set("X-Content-Type-Options", "nosniff")
		// Disallow framing (clickjacking).
		h.Set("X-Frame-Options", "DENY")
		// Don't leak full URLs (which may carry ids) to third parties.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Enforce HTTPS once the connection is already secure. Harmless over
		// plaintext dev (the header is ignored by browsers on http://).
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
