package main

import (
	"log/slog"
	"net/http"
	"strconv"
)

// mountNotificationDeliveries exposes the user's notification delivery audit —
// what was sent / suppressed / failed on each side-channel, newest first.
//
//	GET /api/notifications/deliveries?limit=
func mountNotificationDeliveries(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.NotificationDeliveries == nil {
		return
	}

	mux.HandleFunc("GET /api/notifications/deliveries", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		limit := 100
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
			limit = min(v, 500)
		}
		rows, err := app.NotificationDeliveries.ListForUser(r.Context(), userID, limit)
		if err != nil {
			logger.Error("list deliveries failed", "user", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, d := range rows {
			var wh string
			if d.WebhookEndpoint != nil {
				wh = d.WebhookEndpoint.String()
			}
			out = append(out, map[string]any{
				"event_id":         d.EventID.String(),
				"channel":          string(d.Channel),
				"webhook_endpoint": wh,
				"email_address":    d.EmailAddress,
				"status":           string(d.Status),
				"http_status_code": d.HTTPStatusCode,
				"error":            d.ErrorMessage,
				"attempt":          d.Attempt,
				"attempted_at":     d.AttemptedAt.UTC(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
	})

	logger.Info("notification deliveries endpoint mounted")
}
