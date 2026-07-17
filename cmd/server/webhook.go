package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// mountWebhookForwarder wires the generic provider-agnostic webhook
// endpoints on the mux:
//
//	POST /webhooks/{provider}   — raw forward to cairn.webhooks.<provider>.event
//	GET  /webhooks/{provider}   — request/reply to cairn.webhooks.<provider>.verify
//
// The core knows NOTHING about envelope shapes, signature algorithms,
// or handshake protocols. Workers implement provider-specific
// verification + decode via workersdk.WebhookEvent + WebhookVerify.
//
// This is the architecturally clean design: provider-specific code
// lives in the worker, the core is just a transport adapter. The webhook is
// "owned" by the worker — it advertises (via its presence heartbeat, field
// `webhooks`) that it has registered WebhookEvent/WebhookVerify handlers for
// its provider. The app's worker-onboarding UI then surfaces this instance's
// `/webhooks/<provider>` URL for that provider. The core never enumerates
// providers itself; which providers have a webhook endpoint is entirely a
// function of which workers are connected and what they advertise.
//
// Security caveats:
//
//   - HMAC signature validation (GitHub, Stripe, etc.) happens in the
//     worker after the raw body is delivered via NATS. The core forwards
//     all headers so the worker has what it needs.
//   - Verify-token validation (Strava's hub.verify_token) happens in the
//     worker's WebhookVerify handler.
//   - Bodies are capped at maxBodyBytes (1 MiB by default) — past that
//     the core returns 413 without forwarding.
//   - When no worker is subscribed for the provider, the verify request
//     times out and core returns 503. The POST path still forwards
//     (JetStream WorkQueue retains the message; worker picks it up when
//     it connects).
//
// Gated on app.NATSBus — when NATS isn't wired, both endpoints return 503.
func mountWebhookForwarder(mux *http.ServeMux, app *App, logger *slog.Logger) {
	log := logger.With("component", "webhook_forwarder")

	// These endpoints are unauthenticated (providers can't carry our session).
	// Cap the per-IP rate so a single source can't flood JetStream. 120/min is
	// far above any real provider's webhook cadence.
	limiter := newIPRateLimiter(120, nil)
	limited := func(next func(http.ResponseWriter, *http.Request, *App, *slog.Logger)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(clientIP(r)) {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			next(w, r, app, log)
		}
	}

	mux.HandleFunc("POST /webhooks/{provider}", limited(handleWebhookPost))
	mux.HandleFunc("GET /webhooks/{provider}", limited(handleWebhookGet))
}

const (
	// maxBodyBytes caps the size of a POSTed webhook body. Providers
	// typically send <10 KB; 1 MiB is a generous ceiling that defends
	// against malformed clients trying to OOM the server.
	maxBodyBytes = 1 << 20

	// verifyTimeout caps how long we'll wait for a worker to respond
	// to a GET-handshake forward. Most providers expect a sub-second
	// response; 3s gives reasonable headroom while still failing fast
	// when no worker is connected.
	verifyTimeout = 3 * time.Second
)

func handleWebhookPost(
	w http.ResponseWriter,
	r *http.Request,
	app *App,
	log *slog.Logger,
) {
	if app.NATSBus == nil {
		http.Error(w, "webhook forwarder not wired: NATS adapter not running", http.StatusServiceUnavailable)
		return
	}

	provider := r.PathValue("provider")
	if !validProviderPath(provider) {
		http.Error(w, "invalid provider", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusRequestEntityTooLarge)
		return
	}

	// Build the envelope: original headers (so worker can validate HMAC
	// signatures, etc.) + source-IP for logging, + the raw body.
	envelope := webhookEventEnvelope{
		Provider: provider,
		Headers:  flattenHeaders(r.Header),
		Body:     body,
	}
	envelope.Headers["x-webhook-source-addr"] = r.RemoteAddr
	envelope.Headers["x-webhook-received-at"] = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(envelope)
	if err != nil {
		http.Error(w, "encode envelope: "+err.Error(), http.StatusInternalServerError)
		return
	}

	subject := "cairn.webhooks." + provider + ".event"
	msgID := provider + ":" + envelope.Headers["x-webhook-received-at"] // best-effort dedup; provider-aware msgID would be better but needs decode
	// Bound the publish so an unauthenticated webhook can't hang a server
	// goroutine when JetStream is slow/unreachable.
	pubCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := app.NATSBus.Publish(pubCtx, subject, msgID, payload); err != nil {
		log.Warn("forward webhook event failed",
			"provider", provider, "error", err)
		http.Error(w, "forward failed", http.StatusBadGateway)
		return
	}
	log.Info("webhook event forwarded",
		"provider", provider, "subject", subject, "size_bytes", len(body))
	w.WriteHeader(http.StatusOK)
}

func handleWebhookGet(
	w http.ResponseWriter,
	r *http.Request,
	app *App,
	log *slog.Logger,
) {
	if app.NATSBus == nil {
		http.Error(w, "webhook forwarder not wired: NATS adapter not running", http.StatusServiceUnavailable)
		return
	}

	provider := r.PathValue("provider")
	if !validProviderPath(provider) {
		http.Error(w, "invalid provider", http.StatusBadRequest)
		return
	}

	req := webhookVerifyRequest{
		Method: r.Method,
		Query:  flattenQuery(r.URL.Query()),
	}
	payload, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "encode verify request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	subject := "cairn.webhooks." + provider + ".verify"
	ctx, cancel := context.WithTimeout(r.Context(), verifyTimeout)
	defer cancel()

	respBytes, err := app.NATSBus.Request(ctx, subject, payload, verifyTimeout)
	if err != nil {
		log.Warn("verify request to worker failed",
			"provider", provider, "subject", subject, "error", err)
		http.Error(w, "no worker available to handle verification", http.StatusServiceUnavailable)
		return
	}

	var resp webhookVerifyResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		log.Warn("decode verify response failed", "provider", provider, "error", err)
		http.Error(w, "invalid response from worker", http.StatusBadGateway)
		return
	}

	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	if resp.ContentType == "" {
		resp.ContentType = "application/json"
	}
	w.Header().Set("content-type", resp.ContentType)
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

// ---------------------------------------------------------------------------
// Wire types — mirror workersdk's WebhookEvent / WebhookVerifyRequest /
// WebhookVerifyResponse JSON shapes. Kept inline (not imported) so the
// core has zero dependency on the SDK package — the JSON contract is
// the only coupling.
// ---------------------------------------------------------------------------

type webhookEventEnvelope struct {
	Provider string            `json:"provider"`
	Headers  map[string]string `json:"headers"`
	Body     []byte            `json:"body"`
}

type webhookVerifyRequest struct {
	Method string            `json:"method"`
	Query  map[string]string `json:"query"`
}

type webhookVerifyResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validProviderPath ensures the path parameter is safe to use as a
// NATS subject token. NATS subjects can't contain whitespace, dots,
// stars, or greaters. We accept ASCII alnum + hyphen + underscore, no
// special chars.
func validProviderPath(p string) bool {
	if p == "" || len(p) > 64 {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// flattenHeaders takes the multi-value http.Header and returns a single-
// value map with lowercased keys. Providers don't repeat header names
// in webhook envelopes, so collapsing is safe.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[lowerASCII(k)] = vals[0]
		}
	}
	return out
}

// flattenQuery same idea for URL query params.
func flattenQuery(q map[string][]string) map[string]string {
	out := make(map[string]string, len(q))
	for k, vals := range q {
		if len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
