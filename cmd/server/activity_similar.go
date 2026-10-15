package main

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountActivitySimilar exposes "activities like this one" — other activities
// that cover roughly the same route (shared segment efforts) — plus the metrics
// the progression view needs to show getting-faster/slower over the repeats.
//
//	GET /api/activities/{id}/similar
//	  → { target_segments, activities:[{id,is_current,title,type,start_time,
//	         distance_m,moving_s,elapsed_s,avg_heart_rate,avg_power,pace_s_per_km,
//	         shared_segments,overlap_pct}],
//	      summary:{count,best_moving_s,current_moving_s,current_rank,is_best} }
func mountActivitySimilar(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Segments == nil {
		return
	}

	mux.HandleFunc("GET /api/activities/{id}/similar", activitySimilarHandler(app, logger))

	logger.Info("activity similar endpoint mounted")
}

// activitySimilarRow is one entry of the similar-activities group.
type activitySimilarRow struct {
	sa        domain.SimilarActivity
	isCurrent bool
}

func activitySimilarHandler(app *App, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(id))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// The matcher needs start coordinates + distance. Without them we can't
		// tell "same route", so return an empty group (just the current activity).
		matches, err := activitySimilarMatches(r, app, act, userID)
		if err != nil {
			logger.Error("find similar failed", "activity", act.ID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}

		targetSegments := 0
		if len(matches) > 0 {
			targetSegments = matches[0].TargetSegments
		}

		group := activitySimilarGroup(act, matches, targetSegments)

		bestMoving, currentMoving, currentRank := activitySimilarRank(group)

		out := make([]map[string]any, 0, len(group))
		for _, g := range group {
			out = append(out, similarRowJSON(g.sa, g.isCurrent, targetSegments))
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"target_segments": targetSegments,
			"activities":      out,
			"summary": map[string]any{
				"count":            len(group),
				"best_moving_s":    bestMoving,
				"current_moving_s": currentMoving,
				"current_rank":     currentRank,
				"is_best":          currentMoving > 0 && currentMoving == bestMoving,
			},
		})
	}
}

// activitySimilarMatches finds same-route activities. The matcher needs start coordinates + distance.
// Without them we can't tell "same route", so return an empty group (just the current activity).
func activitySimilarMatches(r *http.Request, app *App, act domain.Activity, userID domain.UserID) ([]domain.SimilarActivity, error) {
	if act.StartLat == nil || act.StartLng == nil || act.Summary.DistanceM == nil {
		return nil, nil
	}
	return app.Segments.FindSimilarActivities(
		r.Context(), act.ID, userID,
		*act.StartLat, *act.StartLng, *act.Summary.DistanceM, string(act.Type),
	)
}

// activitySimilarGroup assembles the full group including the reference activity, chronological.
func activitySimilarGroup(act domain.Activity, matches []domain.SimilarActivity, targetSegments int) []activitySimilarRow {
	group := make([]activitySimilarRow, 0, len(matches)+1)
	group = append(group, activitySimilarRow{sa: toSimilar(act, targetSegments), isCurrent: true})
	for _, m := range matches {
		group = append(group, activitySimilarRow{sa: m})
	}
	sort.Slice(group, func(i, j int) bool {
		return group[i].sa.StartTime.Before(group[j].sa.StartTime)
	})
	return group
}

// activitySimilarRank ranks by moving time (ascending = faster). Best = rank 1.
func activitySimilarRank(group []activitySimilarRow) (bestMoving, currentMoving int64, currentRank int) {
	for _, g := range group {
		if g.sa.MovingS > 0 && (bestMoving == 0 || g.sa.MovingS < bestMoving) {
			bestMoving = g.sa.MovingS
		}
	}
	// rank = 1 + count of activities strictly faster than current
	for _, g := range group {
		if g.isCurrent {
			currentMoving = g.sa.MovingS
		}
	}
	for _, g := range group {
		if g.sa.MovingS > 0 && currentMoving > 0 && g.sa.MovingS < currentMoving {
			currentRank++
		}
	}
	currentRank++ // 1-based
	return bestMoving, currentMoving, currentRank
}

// toSimilar projects the reference activity into the SimilarActivity shape so
// it sits in the same chronological group as its matches.
func toSimilar(a domain.Activity, targetSegments int) domain.SimilarActivity {
	sa := domain.SimilarActivity{
		ActivityID:     a.ID,
		Title:          a.Title,
		Type:           string(a.Type),
		StartTime:      a.StartTime,
		DistanceM:      a.Summary.DistanceM,
		MovingS:        int64(a.MovingDuration.Seconds()),
		ElapsedS:       int64(a.ElapsedDuration.Seconds()),
		SharedSegments: targetSegments,
		TargetSegments: targetSegments,
	}
	if a.Summary.AvgHeartRateBpm != nil {
		v := *a.Summary.AvgHeartRateBpm
		sa.AvgHeartRateBpm = &v
	}
	if a.Summary.AvgPowerW != nil {
		v := *a.Summary.AvgPowerW
		sa.AvgPowerW = &v
	}
	return sa
}

func similarRowJSON(sa domain.SimilarActivity, isCurrent bool, targetSegments int) map[string]any {
	var paceSPerKm *float64
	if sa.DistanceM != nil && *sa.DistanceM > 0 && sa.MovingS > 0 {
		p := float64(sa.MovingS) / (*sa.DistanceM / 1000.0)
		paceSPerKm = &p
	}
	overlap := 0.0
	if targetSegments > 0 {
		overlap = float64(sa.SharedSegments) / float64(targetSegments)
	}
	return map[string]any{
		"id":              sa.ActivityID.String(),
		"is_current":      isCurrent,
		"title":           sa.Title,
		"type":            sa.Type,
		"start_time":      sa.StartTime.UTC(),
		"distance_m":      sa.DistanceM,
		"moving_s":        sa.MovingS,
		"elapsed_s":       sa.ElapsedS,
		"avg_heart_rate":  sa.AvgHeartRateBpm,
		"avg_power":       sa.AvgPowerW,
		"pace_s_per_km":   paceSPerKm,
		"shared_segments": sa.SharedSegments,
		"overlap_pct":     overlap,
	}
}
