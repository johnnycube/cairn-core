package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountNotificationQuietHours exposes the user's do-not-disturb window for
// side-channel notifications (email/webhook). In-app is never suppressed, and
// error-severity notifications are always delivered.
//
//	GET /api/notifications/quiet-hours
//	PUT /api/notifications/quiet-hours  { enabled, start_minute, end_minute, days_of_week, tz }
func mountNotificationQuietHours(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.QuietHours == nil {
		return
	}

	mux.HandleFunc("GET /api/notifications/quiet-hours", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		q, err := app.QuietHours.Get(r.Context(), userID)
		if err != nil {
			logger.Error("get quiet hours failed", "user", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		days := q.DaysOfWeek
		if days == nil {
			days = []int{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":      q.Enabled,
			"start_minute": q.StartMinute,
			"end_minute":   q.EndMinute,
			"days_of_week": days,
			"tz":           q.TZ,
		})
	})

	mux.HandleFunc("PUT /api/notifications/quiet-hours", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Enabled     bool   `json:"enabled"`
			StartMinute int    `json:"start_minute"`
			EndMinute   int    `json:"end_minute"`
			DaysOfWeek  []int  `json:"days_of_week"`
			TZ          string `json:"tz"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body.StartMinute < 0 || body.StartMinute >= 1440 || body.EndMinute < 0 || body.EndMinute >= 1440 {
			http.Error(w, "start_minute/end_minute must be in [0,1440)", http.StatusBadRequest)
			return
		}
		for _, d := range body.DaysOfWeek {
			if d < 0 || d > 6 {
				http.Error(w, "days_of_week entries must be 0..6 (Sun..Sat)", http.StatusBadRequest)
				return
			}
		}
		if body.TZ == "" {
			body.TZ = "UTC"
		}
		if _, err := time.LoadLocation(body.TZ); err != nil {
			http.Error(w, "invalid tz (use an IANA name like Europe/Berlin)", http.StatusBadRequest)
			return
		}
		if err := app.QuietHours.Upsert(r.Context(), domain.QuietHours{
			UserID:      userID,
			Enabled:     body.Enabled,
			StartMinute: body.StartMinute,
			EndMinute:   body.EndMinute,
			DaysOfWeek:  body.DaysOfWeek,
			TZ:          body.TZ,
		}); err != nil {
			logger.Error("upsert quiet hours failed", "user", userID, "error", err)
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("notification quiet-hours endpoints mounted")
}
