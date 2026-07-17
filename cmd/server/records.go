package main

import (
	"log/slog"
	"net/http"
)

// mountRecords serves cross-provider personal records: all-time best per
// (activity type, metric, window). Computed on read from best_efforts.
//
//	GET /api/records
func mountRecords(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.BestEfforts == nil {
		return
	}
	mux.HandleFunc("GET /api/records", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		recs, err := app.BestEfforts.ListPersonalRecords(r.Context(), userID)
		if err != nil {
			logger.Error("list personal records failed", "user_id", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(recs))
		for _, p := range recs {
			out = append(out, map[string]any{
				"activity_type":  string(p.ActivityType),
				"metric":         string(p.Metric),
				"window_kind":    string(p.WindowKind),
				"window_value":   p.WindowValue,
				"achieved_value": p.AchievedValue,
				"activity_id":    p.ActivityID.String(),
				"timestamp":      p.Timestamp.UTC(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": out})
	})
}
