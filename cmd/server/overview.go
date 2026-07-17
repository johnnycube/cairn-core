package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/johnnycube/cairn-core/internal/port"
)

// mountOverview powers the landing dashboard: lifetime totals, a per-sport
// breakdown, and the latest activities.
//
//	GET /api/overview
//	  → { totals:{count,distance_m,moving_s,elevation_gain_m},
//	      by_sport:[{value,count}], recent:[activity...] }
func mountOverview(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		totals, err := app.Activities.ActivityTotals(r.Context(), userID)
		if err != nil {
			http.Error(w, "totals failed", http.StatusInternalServerError)
			return
		}
		types, _, _, _ := app.Activities.ActivityFacets(r.Context(), userID, port.ActivityListFilter{})
		recent, _ := app.Activities.ListRecentActivitiesForUser(r.Context(), userID, 6)

		// This-week / last-week aggregates (UTC, Monday-start weeks).
		now := time.Now().UTC()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		offset := (int(today.Weekday()) + 6) % 7 // Mon=0 … Sun=6
		thisWeekStart := today.AddDate(0, 0, -offset)
		lastWeekStart := thisWeekStart.AddDate(0, 0, -7)
		thisWeek, _ := app.Activities.ActivityTotalsForRange(r.Context(), userID, thisWeekStart, thisWeekStart.AddDate(0, 0, 7))
		lastWeek, _ := app.Activities.ActivityTotalsForRange(r.Context(), userID, lastWeekStart, thisWeekStart)
		weekJSON := func(t port.ActivityTotalsResult) map[string]any {
			return map[string]any{
				"count": t.Count, "distance_m": t.DistanceM,
				"moving_s": t.MovingS, "elevation_gain_m": t.ElevationGainM,
			}
		}

		rec := make([]feedActivity, 0, len(recent))
		for _, a := range recent {
			rec = append(rec, feedActivity{
				ID:               a.ID.String(),
				Title:            a.Title,
				Type:             string(a.Type),
				Discipline:       string(a.Discipline),
				StartTime:        a.StartTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
				Timezone:         a.Timezone,
				ElapsedDurationS: int64(a.ElapsedDuration.Seconds()),
				DistanceM:        a.Summary.DistanceM,
				ElevationGainM:   a.Summary.ElevationGainM,
				TSS:              a.Summary.TSS,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"totals": map[string]any{
				"count":            totals.Count,
				"distance_m":       totals.DistanceM,
				"moving_s":         totals.MovingS,
				"elevation_gain_m": totals.ElevationGainM,
			},
			"by_sport":  toFeedFacets(types),
			"recent":    rec,
			"this_week": weekJSON(thisWeek),
			"last_week": weekJSON(lastWeek),
		})
	})

	logger.Info("overview endpoint mounted")
}
