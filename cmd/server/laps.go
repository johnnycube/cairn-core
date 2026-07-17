package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountLaps exposes an activity's provider-reported laps (splits) for the
// activity-detail page. Laps live on the source payload; the merge engine picks
// one source's lap segmentation, so we surface the laps from whichever attached
// source has them (richest wins on ties).
//
//	GET /api/activities/{id}/laps
//	  → { laps: [{ index, label, start_offset_s, elapsed_s, moving_s,
//	              distance_m, avg_speed_mps, avg_heart_rate, avg_power,
//	              avg_cadence, elevation_gain_m }] }
func mountLaps(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/activities/{id}/laps", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		activityID := domain.ActivityID(id)

		act, err := app.Activities.GetActivity(r.Context(), activityID)
		if err != nil || act.UserID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		sources, err := app.Activities.ListSourcesForActivity(r.Context(), activityID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		// Pick the source with the most laps (the merge engine's lap winner is a
		// single source; "most laps" is a robust proxy without depending on
		// provenance internals).
		var laps []domain.ActivityLap
		for _, s := range sources {
			if len(s.Parsed.Laps) > len(laps) {
				laps = s.Parsed.Laps
			}
		}

		out := make([]map[string]any, 0, len(laps))
		for i, l := range laps {
			idx := l.Index
			if idx == 0 {
				idx = i + 1
			}
			out = append(out, map[string]any{
				"index":            idx,
				"label":            l.Label,
				"start_offset_s":   int64(l.StartOffset.Seconds()),
				"elapsed_s":        int64(l.ElapsedDuration.Seconds()),
				"moving_s":         int64(l.MovingDuration.Seconds()),
				"distance_m":       l.DistanceM,
				"avg_speed_mps":    l.AvgSpeedMps,
				"avg_heart_rate":   l.AvgHeartRateBpm,
				"avg_power":        l.AvgPowerW,
				"avg_cadence":      l.AvgCadence,
				"elevation_gain_m": l.ElevationGainM,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"laps": out})
	})

	logger.Info("laps endpoint mounted")
}
