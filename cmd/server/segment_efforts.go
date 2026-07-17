package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountSegmentEfforts exposes an activity's segment efforts (joined to their
// segment) as JSON for the activity-detail page.
//
//	GET /api/activities/{id}/segment-efforts
//	  → { efforts: [{ segment_name, segment_distance_m, climb_category,
//	                  elapsed_s, moving_s, avg_heart_rate, avg_power,
//	                  is_personal_record, personal_rank }] }
func mountSegmentEfforts(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Segments == nil {
		return
	}

	mux.HandleFunc("GET /api/activities/{id}/segment-efforts", func(w http.ResponseWriter, r *http.Request) {
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

		// Ownership: the activity must belong to the caller.
		act, err := app.Activities.GetActivity(r.Context(), activityID)
		if err != nil || act.UserID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		efforts, err := app.Segments.ListEffortsForActivity(r.Context(), activityID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// streamCache: an effort's StartOffset/EndOffset are sample indices into
		// its source's RAW stream. The detail page only has the downsampled
		// stream, so we convert here to resolution-independent values — time
		// offset (seconds since stream start, matching the track's `t`) and
		// cumulative distance — that the frontend can map onto its elevation
		// profile. One raw-stream read per source, cached.
		streamCache := map[domain.SourceID][]domain.StreamSample{}
		samplesFor := func(srcID domain.SourceID) []domain.StreamSample {
			if s, ok := streamCache[srcID]; ok {
				return s
			}
			st, err := app.Streams.QueryStream(r.Context(), domain.StreamQuery{
				ActivitySourceID: srcID,
				Resolution:       domain.StreamResolutionRaw,
			})
			if err != nil {
				streamCache[srcID] = nil
				return nil
			}
			streamCache[srcID] = st.Samples
			return st.Samples
		}

		segs := map[domain.SegmentID]domain.Segment{}
		out := make([]map[string]any, 0, len(efforts))
		for _, e := range efforts {
			seg, ok := segs[e.SegmentID]
			if !ok {
				seg, _ = app.Segments.GetSegment(r.Context(), e.SegmentID)
				segs[e.SegmentID] = seg
			}
			var climb any
			if seg.ClimbCategory != "" && seg.ClimbCategory != domain.ClimbCategoryNone {
				climb = string(seg.ClimbCategory)
			}

			row := map[string]any{
				"segment_id":         e.SegmentID.String(),
				"segment_name":       seg.Name,
				"segment_distance_m": seg.DistanceM,
				"climb_category":     climb,
				"elapsed_s":          e.ElapsedS,
				"moving_s":           e.MovingS,
				"avg_heart_rate":     e.AvgHeartRateBpm,
				"avg_power":          e.AvgPowerW,
				"is_personal_record": e.IsPersonalRecord,
				"personal_rank":      e.PersonalRank,
			}
			// Map the raw-stream offsets to time + distance so the frontend can
			// locate the segment on its (downsampled) elevation profile.
			if s := samplesFor(e.ActivitySourceID); len(s) > 0 {
				if e.StartOffset >= 0 && e.StartOffset < len(s) && e.EndOffset >= 0 && e.EndOffset < len(s) {
					base := s[0].Timestamp
					row["start_offset_s"] = s[e.StartOffset].Timestamp.Sub(base).Seconds()
					row["end_offset_s"] = s[e.EndOffset].Timestamp.Sub(base).Seconds()
					if d := s[e.StartOffset].DistanceM; d != nil {
						row["start_distance_m"] = *d
					}
					if d := s[e.EndOffset].DistanceM; d != nil {
						row["end_distance_m"] = *d
					}
				}
			}
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"efforts": out})
	})

	logger.Info("segment-efforts endpoint mounted")
}
