package main

import (
	"log/slog"
	"net/http"
	"strconv"
)

var validEffortMetrics = map[string]bool{
	"pace": true, "speed": true, "power": true, "heart_rate": true, "vam": true,
}

// mountBestEffortHistory exposes the progression of one best-effort bucket
// (metric + window) across all the user's activities — the data behind the
// best-effort detail page (e.g. "Power 20 min" over time).
//
//	GET /api/best-efforts/history?metric=&window_kind=&window_value=
//	  → { metric, window_kind, window_value, best, items:[{activity_id,title,start_time,achieved_value,is_best}] }
func mountBestEffortHistory(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.BestEfforts == nil {
		return
	}

	mux.HandleFunc("GET /api/best-efforts/history", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		activityType := q.Get("type")
		metric := q.Get("metric")
		windowKind := q.Get("window_kind")
		windowValue, _ := strconv.Atoi(q.Get("window_value"))
		if activityType == "" || !validEffortMetrics[metric] || (windowKind != "distance" && windowKind != "duration") || windowValue <= 0 {
			http.Error(w, "type, metric, window_kind (distance|duration) and window_value are required", http.StatusBadRequest)
			return
		}

		items, err := app.BestEfforts.ListBestEffortHistory(r.Context(), userID, activityType, metric, windowKind, windowValue)
		if err != nil {
			logger.Error("best-effort history failed", "user", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}

		// All-time best: min for pace, max otherwise.
		best := 0.0
		for i, it := range items {
			if i == 0 {
				best = it.AchievedValue
				continue
			}
			if metric == "pace" {
				if it.AchievedValue < best {
					best = it.AchievedValue
				}
			} else if it.AchievedValue > best {
				best = it.AchievedValue
			}
		}

		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"activity_id":    it.ActivityID.String(),
				"title":          it.Title,
				"start_time":     it.StartTime.UTC(),
				"achieved_value": it.AchievedValue,
				"is_best":        len(items) > 0 && it.AchievedValue == best,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"type":         activityType,
			"metric":       metric,
			"window_kind":  windowKind,
			"window_value": windowValue,
			"best":         best,
			"count":        len(items),
			"items":        out,
		})
	})

	logger.Info("best-effort history endpoint mounted")
}
