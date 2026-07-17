package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// mountSegmentDetail exposes a single segment with the viewer's full effort
// history, for the segment detail page (map + effort list + time-trend chart).
//
//	GET /api/segments/{id}
//	  → { segment:{id,name,distance_m,activity_type,climb_category,polyline,
//	               polyline_precision,elevation_gain_m,avg_grade,max_grade},
//	      efforts:[{id,activity_id,start_time,elapsed_s,moving_s,avg_heart_rate,
//	                avg_power,personal_rank,is_personal_record}] }
func mountSegmentDetail(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Segments == nil {
		return
	}

	mux.HandleFunc("GET /api/segments/{id}", func(w http.ResponseWriter, r *http.Request) {
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
		segID := domain.SegmentID(id)

		seg, err := app.Segments.GetSegment(r.Context(), segID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// The viewer's own efforts on this segment, newest first for the list;
		// the chart re-sorts chronologically client-side.
		efforts, err := app.Segments.ListEffortsForSegment(r.Context(), segID, userID, port.LeaderboardScopePersonalOnly, 1000, 0)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		// Visibility: a viewer sees a segment they've actually ridden, or one
		// they own (native). Otherwise it's not theirs to see.
		owns := seg.OwnerUserID != nil && *seg.OwnerUserID == userID
		if len(efforts) == 0 && !owns {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		out := make([]map[string]any, 0, len(efforts))
		for _, e := range efforts {
			out = append(out, map[string]any{
				"id":                 e.ID.String(),
				"activity_id":        e.ActivityID.String(),
				"start_time":         e.StartTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
				"elapsed_s":          int64(e.ElapsedS),
				"moving_s":           int64(e.MovingS),
				"avg_heart_rate":     e.AvgHeartRateBpm,
				"avg_power":          e.AvgPowerW,
				"personal_rank":      e.PersonalRank,
				"is_personal_record": e.IsPersonalRecord,
			})
		}

		var climb any
		if seg.ClimbCategory != "" && seg.ClimbCategory != domain.ClimbCategoryNone {
			climb = string(seg.ClimbCategory)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"segment": map[string]any{
				"id":                 seg.ID.String(),
				"name":               seg.Name,
				"distance_m":         seg.DistanceM,
				"activity_type":      string(seg.ActivityType),
				"climb_category":     climb,
				"polyline":           seg.Polyline,
				"polyline_precision": seg.PolylinePrecision,
				"elevation_gain_m":   seg.ElevationGainM,
				"avg_grade":          seg.AvgGrade,
				"max_grade":          seg.MaxGrade,
			},
			"efforts": out,
		})
	})

	logger.Info("segment detail endpoint mounted")
}
