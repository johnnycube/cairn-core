package main

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain/capability"
	"github.com/johnnycube/cairn-core/internal/domain/health"
)

// mountHealth serves merged daily health metrics (raw per-provider samples
// merged per type/day by the provider cascade). Empty until a health worker imports.
//
//	GET /api/health?type=HRV&days=90
func mountHealth(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Health == nil {
		return
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		dt := capability.DataType(r.URL.Query().Get("type"))
		if dt.CategoryOf() != capability.CategoryTimeSeries {
			http.Error(w, "type must be a time-series health data type", http.StatusBadRequest)
			return
		}
		days := 90
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 3650 {
				days = n
			}
		}
		to := time.Now().UTC()
		from := to.AddDate(0, 0, -days)

		samples, err := app.Health.ListSamples(r.Context(), userID, dt, from, to)
		if err != nil {
			logger.Error("list health samples failed", "user_id", userID, "type", dt, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		// Provider cascade for health is the AnyProvider default for now (latest
		// wins); a per-type health priority can layer on later like merge policy.
		merged := health.MergeDaily(samples, nil)

		out := make([]map[string]any, 0, len(merged))
		for _, m := range merged {
			out = append(out, map[string]any{
				"day":      m.Day.Format("2006-01-02"),
				"value":    m.Value,
				"unit":     m.Unit,
				"provider": m.Provider,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": string(dt), "series": out})
	})
}
