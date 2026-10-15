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
	"github.com/johnnycube/cairn-core/internal/safehttp"
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
	h := &notifWebhooksAPI{app: app, logger: logger}
	mux.HandleFunc("GET /api/notifications/webhooks", h.list)
	mux.HandleFunc("POST /api/notifications/webhooks", h.create)
	mux.HandleFunc("PUT /api/notifications/webhooks/{id}", h.update)
	mux.HandleFunc("POST /api/notifications/webhooks/{id}/rotate-secret", h.rotateSecret)
	mux.HandleFunc("POST /api/notifications/webhooks/{id}/test", h.test)
	mux.HandleFunc("DELETE /api/notifications/webhooks/{id}", h.delete)

	logger.Info("notification webhook endpoints mounted")
}

// notifWebhooksAPI holds the dependencies of the webhook CRUD handlers.
type notifWebhooksAPI struct {
	app    *App
	logger *slog.Logger
}

// userAndID resolves the session user and the {id} path value or writes an error.
func (h *notifWebhooksAPI) userAndID(w http.ResponseWriter, r *http.Request) (domain.UserID, domain.WebhookEndpointID, bool) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return userID, domain.WebhookEndpointID{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return userID, domain.WebhookEndpointID{}, false
	}
	return userID, domain.WebhookEndpointID(id), true
}

// decodeUpsert reads and validates a create/update body or writes an error.
func (h *notifWebhooksAPI) decodeUpsert(w http.ResponseWriter, r *http.Request) (webhookUpsertBody, bool) {
	var body webhookUpsertBody
	if !requireJSON(w, r) {
		return body, false
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return body, false
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	if body.Name == "" || !isHTTPURL(body.URL) {
		http.Error(w, "name and a valid http(s) url are required", http.StatusBadRequest)
		return body, false
	}
	if body.MinSeverity == "" {
		body.MinSeverity = string(domain.NotificationSeverityInfo)
	}
	if !validMinSeverity(body.MinSeverity) {
		http.Error(w, "invalid min_severity", http.StatusBadRequest)
		return body, false
	}
	return body, true
}

// writeStoreErr maps a repo error to 404/500; it reports whether one was written.
func (h *notifWebhooksAPI) writeStoreErr(w http.ResponseWriter, err error, logMsg, httpMsg string, id domain.WebhookEndpointID) bool {
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return true
	}
	if err != nil {
		h.logger.Error(logMsg, "id", id, "error", err)
		http.Error(w, httpMsg, http.StatusInternalServerError)
		return true
	}
	return false
}

func (h *notifWebhooksAPI) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	eps, err := h.app.WebhookEndpoints.ListForUser(r.Context(), userID)
	if err != nil {
		h.logger.Error("list webhooks failed", "user", userID, "error", err)
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		out = append(out, webhookEndpointJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": out})
}

func (h *notifWebhooksAPI) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	body, ok := h.decodeUpsert(w, r)
	if !ok {
		return
	}
	secret, err := genWebhookSecret()
	if err != nil {
		http.Error(w, "secret generation failed", http.StatusInternalServerError)
		return
	}
	created, err := h.app.WebhookEndpoints.Create(r.Context(), domain.WebhookEndpoint{
		UserID:        userID,
		Name:          body.Name,
		URL:           body.URL,
		SigningSecret: secret,
		EventTypes:    body.toEventTypes(),
		MinSeverity:   domain.NotificationSeverity(body.MinSeverity),
		Enabled:       true,
	})
	if err != nil {
		h.logger.Error("create webhook failed", "user", userID, "error", err)
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	resp := webhookEndpointJSON(created)
	resp["secret"] = secret // shown once
	writeJSON(w, http.StatusCreated, resp)
}

func (h *notifWebhooksAPI) update(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.userAndID(w, r)
	if !ok {
		return
	}
	body, ok := h.decodeUpsert(w, r)
	if !ok {
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	err := h.app.WebhookEndpoints.Update(r.Context(), domain.WebhookEndpoint{
		ID:          id,
		UserID:      userID,
		Name:        body.Name,
		URL:         body.URL,
		EventTypes:  body.toEventTypes(),
		MinSeverity: domain.NotificationSeverity(body.MinSeverity),
		Enabled:     enabled,
	})
	if h.writeStoreErr(w, err, "update webhook failed", "update failed", id) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *notifWebhooksAPI) rotateSecret(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.userAndID(w, r)
	if !ok {
		return
	}
	secret, err := genWebhookSecret()
	if err != nil {
		http.Error(w, "secret generation failed", http.StatusInternalServerError)
		return
	}
	err = h.app.WebhookEndpoints.RotateSecret(r.Context(), id, userID, secret)
	if h.writeStoreErr(w, err, "rotate webhook secret failed", "rotate failed", id) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (h *notifWebhooksAPI) test(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.userAndID(w, r)
	if !ok {
		return
	}
	_, err := h.app.WebhookEndpoints.GetByID(r.Context(), id, userID)
	if h.writeStoreErr(w, err, "get webhook failed", "load failed", id) {
		return
	}
	// Synthesise an info notification and run it through the same dispatch
	// path so the test exercises real signing + telemetry.
	nid, _ := uuid.NewV7()
	h.app.DeliverNotifications.Execute(r.Context(), []domain.Notification{{
		ID:           domain.NotificationID(nid),
		UserID:       userID,
		Type:         domain.NotificationTypeActivityImported,
		Severity:     domain.NotificationSeverityInfo,
		TitleI18nKey: "notif.test.title",
		BodyI18nKey:  "notif.test.body",
		CreatedAt:    time.Now().UTC(),
	}})
	// Re-read for fresh telemetry.
	ep, _ := h.app.WebhookEndpoints.GetByID(r.Context(), id, userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"delivered":        ep.LastDeliveryError == "" && ep.LastDeliveryStatusCode >= 200 && ep.LastDeliveryStatusCode < 300,
		"last_status_code": ep.LastDeliveryStatusCode,
		"last_error":       ep.LastDeliveryError,
	})
}

func (h *notifWebhooksAPI) delete(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.userAndID(w, r)
	if !ok {
		return
	}
	err := h.app.WebhookEndpoints.Delete(r.Context(), id, userID)
	if h.writeStoreErr(w, err, "delete webhook failed", "delete failed", id) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		if safehttp.IsBlocked(ip) {
			return false
		}
	}
	return true
}
