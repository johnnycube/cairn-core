package main

import (
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/port"
)

// mountStats powers the Stats page — the data-geek view: lifetime totals,
// per-year breakdown, per-sport split, and personal records.
//
//	GET /api/stats
//	  → { totals, by_sport, by_year, records:{longest_distance, longest_duration} }
func mountStats(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		totals, err := app.Activities.ActivityTotals(r.Context(), userID)
		if err != nil {
			http.Error(w, "stats failed", http.StatusInternalServerError)
			return
		}
		types, disciplines, _, _ := app.Activities.ActivityFacets(r.Context(), userID, port.ActivityListFilter{})
		years, _ := app.Activities.ActivityYearStats(r.Context(), userID)

		yearOut := make([]map[string]any, 0, len(years))
		for _, y := range years {
			yearOut = append(yearOut, map[string]any{
				"year": y.Year, "count": y.Count, "distance_m": y.DistanceM,
				"moving_s": y.MovingS, "elevation_gain_m": y.ElevationGainM,
			})
		}

		record := func(sort string) map[string]any {
			acts, _, err := app.Activities.ListActivitiesFiltered(r.Context(), userID,
				port.ActivityListFilter{Sort: sort, Limit: 1})
			if err != nil || len(acts) == 0 {
				return nil
			}
			a := acts[0]
			return map[string]any{
				"id": a.ID.String(), "title": a.Title,
				"type": string(a.Type), "discipline": string(a.Discipline),
				"distance_m":         deref(a.Summary.DistanceM),
				"elapsed_duration_s": int64(a.ElapsedDuration.Seconds()),
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"totals": map[string]any{
				"count": totals.Count, "distance_m": totals.DistanceM,
				"moving_s": totals.MovingS, "elevation_gain_m": totals.ElevationGainM,
			},
			"by_sport":      toFeedFacets(types),
			"by_discipline": toFeedFacets(disciplines),
			"by_year":       yearOut,
			"records": map[string]any{
				"longest_distance": record("distance_desc"),
				"longest_duration": record("duration_desc"),
			},
		})
	})

	logger.Info("stats endpoint mounted")
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
