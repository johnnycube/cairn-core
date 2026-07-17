package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// notifTypeMeta lists the user-facing notification types and their labels, in
// display order. Kept here (not in domain) because it's UI copy.
var notifTypeMeta = []struct {
	Type  domain.NotificationType
	Label string
}{
	{domain.NotificationTypeSegmentPersonalRecord, "Segment personal records"},
	{domain.NotificationTypeSegmentInstanceRecord, "Segment course records"},
	{domain.NotificationTypeActivityImported, "Activity imported"},
	{domain.NotificationTypeActivityReimported, "Activity updated from source"},
	{domain.NotificationTypeWorkerOffline, "Connector went offline"},
	{domain.NotificationTypeExternalAccountRefreshFailed, "Connection needs re-auth"},
}

// mountNotificationPrefs exposes the per-user email notification preference
// matrix. v1 is the email channel (in-app is always on; webhook/push later).
//
//	GET /api/notifications/preferences        → [{event_type,key,label,email_enabled}]
//	PUT /api/notifications/preferences        { event_type, channel, enabled }
func mountNotificationPrefs(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/notifications/preferences", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		explicit := map[domain.NotificationType]bool{}
		if rows, err := app.NotificationPrefs.ListForUser(r.Context(), userID); err == nil {
			for _, p := range rows {
				if p.Channel == domain.NotificationChannelEmail {
					explicit[p.Type] = p.Enabled
				}
			}
		}
		out := make([]map[string]any, 0, len(notifTypeMeta))
		for _, m := range notifTypeMeta {
			enabled, ok := explicit[m.Type]
			if !ok {
				enabled = domain.DefaultChannelEnabled(m.Type, domain.NotificationChannelEmail)
			}
			out = append(out, map[string]any{
				"event_type":    int(m.Type),
				"key":           m.Type.String(),
				"label":         m.Label,
				"email_enabled": enabled,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"preferences":   out,
			"email_enabled": app.Email != nil, // instance has an email channel at all
		})
	})

	mux.HandleFunc("PUT /api/notifications/preferences", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			EventType int    `json:"event_type"`
			Channel   string `json:"channel"`
			Enabled   bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		ch := domain.NotificationChannel(body.Channel)
		if ch != domain.NotificationChannelEmail {
			http.Error(w, "only the email channel is configurable in v1", http.StatusBadRequest)
			return
		}
		if err := app.NotificationPrefs.Upsert(r.Context(), domain.NotificationPreference{
			UserID:      userID,
			Type:        domain.NotificationType(body.EventType),
			Channel:     ch,
			Enabled:     body.Enabled,
			MinSeverity: domain.NotificationSeverityInfo,
		}); err != nil {
			logger.Error("upsert notification preference failed", "user", userID, "error", err)
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("notification preferences endpoints mounted")
}
