package main

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountSegmentsList exposes the segments landing page data: a headline status
// block + the viewer's most-attempted segments. Both scoped to the caller
// (only segments they have efforts on).
//
//	GET /api/segments?type=<activity_type>&limit=&offset=
//	  → { stats:{segments,efforts,prs,crs,external,native},
//	      segments:[{id,name,activity_type,source,distance_m,elevation_gain_m,
//	                 avg_grade,effort_count,best_elapsed_s,last_effort_at,
//	                 has_pr,has_cr}],
//	      limit, offset, has_more }
func mountSegmentsList(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Segments == nil {
		return
	}

	mux.HandleFunc("GET /api/segments", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		activityType := r.URL.Query().Get("type")
		limit := 50
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
			limit = min(v, 200)
		}
		offset := 0
		if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
			offset = v
		}

		// Stats only on the first page (offset 0) — cheap to skip on paging.
		var stats domain.UserSegmentStats
		if offset == 0 {
			s, err := app.Segments.UserSegmentStats(r.Context(), userID)
			if err != nil {
				logger.Error("segment stats failed", "user", userID, "error", err)
				http.Error(w, "load failed", http.StatusInternalServerError)
				return
			}
			stats = s
		}

		// Fetch one extra to compute has_more without a count query.
		items, err := app.Segments.ListUserSegmentsByEffortCount(r.Context(), userID, activityType, limit+1, offset)
		if err != nil {
			logger.Error("list segments failed", "user", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}

		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"id":               it.ID.String(),
				"name":             it.Name,
				"activity_type":    it.ActivityType,
				"source":           string(it.Source),
				"distance_m":       it.DistanceM,
				"elevation_gain_m": it.ElevationGainM,
				"avg_grade":        it.AvgGrade,
				"effort_count":     it.EffortCount,
				"best_elapsed_s":   it.BestElapsedS,
				"last_effort_at":   it.LastEffortAt.UTC(),
				"has_pr":           it.HasPR,
				"has_cr":           it.HasCR,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"stats": map[string]any{
				"segments": stats.Segments,
				"efforts":  stats.Efforts,
				"prs":      stats.PRs,
				"crs":      stats.CRs,
				"external": stats.External,
				"native":   stats.Native,
			},
			"segments": out,
			"limit":    limit,
			"offset":   offset,
			"has_more": hasMore,
		})
	})

	logger.Info("segments list endpoint mounted")
}
