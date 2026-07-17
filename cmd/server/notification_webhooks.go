package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// genWebhookSecret returns a 32-byte URL-safe secret for HMAC signing.
func genWebhookSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// webhookEndpointJSON is the safe display shape (never the secret).
func webhookEndpointJSON(e domain.WebhookEndpoint) map[string]any {
	types := make([]int, 0, len(e.EventTypes))
	for _, t := range e.EventTypes {
		types = append(types, int(t))
	}
	var lastAt any
	if e.LastDeliveryAt != nil {
		lastAt = e.LastDeliveryAt.UTC()
	}
	return map[string]any{
		"id":                   e.ID.String(),
		"name":                 e.Name,
		"url":                  e.URL,
		"event_types":          types,
		"min_severity":         string(e.MinSeverity),
		"enabled":              e.Enabled,
		"auto_disabled":        e.AutoDisabled,
		"last_delivery_at":     lastAt,
		"last_status_code":     e.LastDeliveryStatusCode,
		"last_error":           e.LastDeliveryError,
		"consecutive_failures": e.ConsecutiveFailures,
		"created_at":           e.CreatedAt.UTC(),
	}
}

type webhookUpsertBody struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	EventTypes  []int  `json:"event_types"`
	MinSeverity string `json:"min_severity"`
	Enabled     *bool  `json:"enabled"`
}

func (b webhookUpsertBody) toEventTypes() []domain.NotificationType {
	out := make([]domain.NotificationType, 0, len(b.EventTypes))
	for _, t := range b.EventTypes {
		out = append(out, domain.NotificationType(t))
	}
	return out
}

func validMinSeverity(s string) bool {
	return domain.NotificationSeverity(s).Valid()
}

// mountNotificationWebhooks wires per-user outbound webhook CRUD. The signing
// secret is shown exactly once (on create + rotate); thereafter only telemetry
// and config are returned.
//
//	GET    /api/notifications/webhooks
//	POST   /api/notifications/webhooks                  → {…, secret} (once)
//	PUT    /api/notifications/webhooks/{id}
//	POST   /api/notifications/webhooks/{id}/rotate-secret → {secret} (once)
//	POST   /api/notifications/webhooks/{id}/test
//	DELETE /api/notifications/webhooks/{id}
func mountNotificationWebhooks(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.WebhookEndpoints == nil {
		return
	}

	parseID := func(w http.ResponseWriter, r *http.Request) (domain.WebhookEndpointID, bool) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return domain.WebhookEndpointID{}, false
		}
		return domain.WebhookEndpointID(id), true
	}

	mux.HandleFunc("GET /api/notifications/webhooks", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		eps, err := app.WebhookEndpoints.ListForUser(r.Context(), userID)
		if err != nil {
			logger.Error("list webhooks failed", "user", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(eps))
		for _, e := range eps {
			out = append(out, webhookEndpointJSON(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"webhooks": out})
	})

	mux.HandleFunc("POST /api/notifications/webhooks", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body webhookUpsertBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		body.URL = strings.TrimSpace(body.URL)
		if body.Name == "" || !isHTTPURL(body.URL) {
			http.Error(w, "name and a valid http(s) url are required", http.StatusBadRequest)
			return
		}
		if body.MinSeverity == "" {
			body.MinSeverity = string(domain.NotificationSeverityInfo)
		}
		if !validMinSeverity(body.MinSeverity) {
			http.Error(w, "invalid min_severity", http.StatusBadRequest)
			return
		}
		secret, err := genWebhookSecret()
		if err != nil {
			http.Error(w, "secret generation failed", http.StatusInternalServerError)
			return
		}
		created, err := app.WebhookEndpoints.Create(r.Context(), domain.WebhookEndpoint{
			UserID:        userID,
			Name:          body.Name,
			URL:           body.URL,
			SigningSecret: secret,
			EventTypes:    body.toEventTypes(),
			MinSeverity:   domain.NotificationSeverity(body.MinSeverity),
			Enabled:       true,
		})
		if err != nil {
			logger.Error("create webhook failed", "user", userID, "error", err)
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		resp := webhookEndpointJSON(created)
		resp["secret"] = secret // shown once
		writeJSON(w, http.StatusCreated, resp)
	})

	mux.HandleFunc("PUT /api/notifications/webhooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body webhookUpsertBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		body.URL = strings.TrimSpace(body.URL)
		if body.Name == "" || !isHTTPURL(body.URL) {
			http.Error(w, "name and a valid http(s) url are required", http.StatusBadRequest)
			return
		}
		if body.MinSeverity == "" {
			body.MinSeverity = string(domain.NotificationSeverityInfo)
		}
		if !validMinSeverity(body.MinSeverity) {
			http.Error(w, "invalid min_severity", http.StatusBadRequest)
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		err := app.WebhookEndpoints.Update(r.Context(), domain.WebhookEndpoint{
			ID:          id,
			UserID:      userID,
			Name:        body.Name,
			URL:         body.URL,
			EventTypes:  body.toEventTypes(),
			MinSeverity: domain.NotificationSeverity(body.MinSeverity),
			Enabled:     enabled,
		})
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("update webhook failed", "id", id, "error", err)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/notifications/webhooks/{id}/rotate-secret", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		secret, err := genWebhookSecret()
		if err != nil {
			http.Error(w, "secret generation failed", http.StatusInternalServerError)
			return
		}
		err = app.WebhookEndpoints.RotateSecret(r.Context(), id, userID, secret)
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("rotate webhook secret failed", "id", id, "error", err)
			http.Error(w, "rotate failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
	})

	mux.HandleFunc("POST /api/notifications/webhooks/{id}/test", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		ep, err := app.WebhookEndpoints.GetByID(r.Context(), id, userID)
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("get webhook failed", "id", id, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		// Synthesise an info notification and run it through the same dispatch
		// path so the test exercises real signing + telemetry.
		nid, _ := uuid.NewV7()
		app.DeliverNotifications.Execute(r.Context(), []domain.Notification{{
			ID:           domain.NotificationID(nid),
			UserID:       userID,
			Type:         domain.NotificationTypeActivityImported,
			Severity:     domain.NotificationSeverityInfo,
			TitleI18nKey: "notif.test.title",
			BodyI18nKey:  "notif.test.body",
			CreatedAt:    time.Now().UTC(),
		}})
		// Re-read for fresh telemetry.
		ep, _ = app.WebhookEndpoints.GetByID(r.Context(), id, userID)
		writeJSON(w, http.StatusOK, map[string]any{
			"delivered":        ep.LastDeliveryError == "" && ep.LastDeliveryStatusCode >= 200 && ep.LastDeliveryStatusCode < 300,
			"last_status_code": ep.LastDeliveryStatusCode,
			"last_error":       ep.LastDeliveryError,
		})
	})

	mux.HandleFunc("DELETE /api/notifications/webhooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		err := app.WebhookEndpoints.Delete(r.Context(), id, userID)
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("delete webhook failed", "id", id, "error", err)
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("notification webhook endpoints mounted")
}

// isHTTPURL validates a user-supplied webhook URL: it must parse, use http(s),
// have a host, and not point at a literal loopback/private/link-local IP. This
// is fast create-time feedback; the authoritative SSRF protection is the
// dial-time IP guard in the delivery client (which also catches hostnames that
// resolve to internal addresses and redirect hops).
func isHTTPURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}
