package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	matchuc "github.com/johnnycube/cairn-core/internal/usecase/match"
)

// mountMergeOverrides wires the manual-override surface. Field pins force which
// source wins a field group; merge/split add must/cannot-link constraints and
// recluster.
//
//	GET/PUT/DELETE /api/activities/{id}/field-source(s)   per-field source pin
//	POST           /api/activities/merge                 join (must-link)
//	POST           /api/activities/{id}/split            split (cannot-link)
func mountMergeOverrides(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.FieldOverrides == nil {
		logger.Info("merge-overrides api not mounted: field override repo not wired")
		return
	}

	// ownActivity resolves the caller's non-deleted activity or writes an error.
	ownActivity := func(w http.ResponseWriter, r *http.Request, idStr string) (domain.Activity, domain.UserID, bool) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return domain.Activity{}, domain.UserID{}, false
		}
		aUUID, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return domain.Activity{}, domain.UserID{}, false
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(aUUID))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return domain.Activity{}, domain.UserID{}, false
		}
		return act, userID, true
	}

	mux.HandleFunc("GET /api/activities/{id}/field-sources", func(w http.ResponseWriter, r *http.Request) {
		act, _, ok := ownActivity(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		pins, err := app.FieldOverrides.ListForActivity(r.Context(), act.ID)
		if err != nil {
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]string, 0, len(pins))
		for _, p := range pins {
			out = append(out, map[string]string{"field": string(p.FieldKey), "source_id": p.SourceID.String()})
		}
		writeJSON(w, http.StatusOK, map[string]any{"overrides": out})
	})

	mux.HandleFunc("PUT /api/activities/{id}/field-source", func(w http.ResponseWriter, r *http.Request) {
		act, _, ok := ownActivity(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Field    string `json:"field"`
			SourceID string `json:"source_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		fg := domain.FieldGroup(body.Field)
		if !validFieldGroup(fg) {
			http.Error(w, "unknown field group", http.StatusBadRequest)
			return
		}
		srcUUID, err := uuid.Parse(body.SourceID)
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		// The pinned source must belong to THIS activity.
		src, err := app.Activities.GetSource(r.Context(), domain.SourceID(srcUUID))
		if err != nil || src.ActivityID != act.ID {
			http.Error(w, "source not on this activity", http.StatusBadRequest)
			return
		}
		if err := app.FieldOverrides.Set(r.Context(), domain.FieldSourceOverride{
			ActivityID: act.ID, FieldKey: fg, SourceID: domain.SourceID(srcUUID),
		}); err != nil {
			http.Error(w, "set failed", http.StatusInternalServerError)
			return
		}
		// Re-derive so the pin takes effect immediately.
		if _, err := app.RecomputeActivity.Execute(r.Context(), act.ID); err != nil {
			logger.Warn("recompute after field pin failed", "activity_id", act.ID, "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "field": body.Field, "source_id": body.SourceID})
	})

	mux.HandleFunc("DELETE /api/activities/{id}/field-source/{field}", func(w http.ResponseWriter, r *http.Request) {
		act, _, ok := ownActivity(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		fg := domain.FieldGroup(r.PathValue("field"))
		if !validFieldGroup(fg) {
			http.Error(w, "unknown field group", http.StatusBadRequest)
			return
		}
		if err := app.FieldOverrides.Delete(r.Context(), act.ID, fg); err != nil {
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		if _, err := app.RecomputeActivity.Execute(r.Context(), act.ID); err != nil {
			logger.Warn("recompute after field unpin failed", "activity_id", act.ID, "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Join two activities into one (must-link). Requires the re-clustering engine.
	mux.HandleFunc("POST /api/activities/merge", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		if app.ReclusterBucket == nil {
			http.Error(w, "re-clustering engine unavailable", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			ActivityA string `json:"activity_a"`
			ActivityB string `json:"activity_b"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		aA, errA := uuid.Parse(body.ActivityA)
		aB, errB := uuid.Parse(body.ActivityB)
		if errA != nil || errB != nil || aA == aB {
			http.Error(w, "two distinct activity ids required", http.StatusBadRequest)
			return
		}
		// Both activities must belong to the caller; pick a representative source
		// from each to anchor the must-link constraint.
		srcA, okA := firstSourceOf(r, app, userID, domain.ActivityID(aA))
		srcB, okB := firstSourceOf(r, app, userID, domain.ActivityID(aB))
		if !okA || !okB {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := app.Activities.AddMatchConstraint(r.Context(), domain.MatchConstraint{
			UserID: userID, SourceA: srcA.ID, SourceB: srcB.ID,
			Kind: domain.ConstraintMustLink, Reason: "user_merge",
		}); err != nil {
			http.Error(w, "constraint write failed", http.StatusInternalServerError)
			return
		}
		// Re-cluster the window spanning both activities so the must-link applies.
		around := srcA.Parsed.StartTime
		if _, err := app.ReclusterBucket.Execute(r.Context(), matchuc.Input{UserID: userID, Around: around}); err != nil {
			logger.Warn("recluster after manual merge failed", "user_id", userID, "error", err)
			http.Error(w, "merge recorded but recluster failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"merged": true})
	})

	// Split a wrongly-merged activity: peel one source off into its own activity
	// (cannot-link it against the activity's other sources, then recluster). The
	// symmetric counterpart to merge/join (brief §13.2).
	mux.HandleFunc("POST /api/activities/{id}/split", func(w http.ResponseWriter, r *http.Request) {
		act, userID, ok := ownActivity(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		if !requireJSON(w, r) {
			return
		}
		if app.ReclusterBucket == nil {
			http.Error(w, "re-clustering engine unavailable", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			SourceID string `json:"source_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		srcUUID, err := uuid.Parse(body.SourceID)
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		peel := domain.SourceID(srcUUID)

		sources, err := app.Activities.ListSourcesForActivity(r.Context(), act.ID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		var peelSrc *domain.ActivitySource
		var others []domain.ActivitySource
		for i := range sources {
			if sources[i].ID == peel {
				peelSrc = &sources[i]
			} else {
				others = append(others, sources[i])
			}
		}
		if peelSrc == nil {
			http.Error(w, "source not on this activity", http.StatusBadRequest)
			return
		}
		if len(others) == 0 {
			http.Error(w, "activity has only one source — nothing to split from", http.StatusConflict)
			return
		}
		// Cannot-link the peeled source against every other source so the matcher
		// separates it into its own cluster.
		for _, o := range others {
			if err := app.Activities.AddMatchConstraint(r.Context(), domain.MatchConstraint{
				UserID: userID, SourceA: peel, SourceB: o.ID,
				Kind: domain.ConstraintCannotLink, Reason: "user_split",
			}); err != nil {
				http.Error(w, "constraint write failed", http.StatusInternalServerError)
				return
			}
		}
		if _, err := app.ReclusterBucket.Execute(r.Context(), matchuc.Input{UserID: userID, Around: peelSrc.Parsed.StartTime}); err != nil {
			logger.Warn("recluster after manual split failed", "user_id", userID, "error", err)
			http.Error(w, "split recorded but recluster failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"split": true})
	})
}

// firstSourceOf returns a representative non-detached source of the caller's
// activity (for anchoring a match constraint).
func firstSourceOf(r *http.Request, app *App, userID domain.UserID, id domain.ActivityID) (domain.ActivitySource, bool) {
	act, err := app.Activities.GetActivity(r.Context(), id)
	if err != nil || act.UserID != userID || act.IsDeleted() {
		return domain.ActivitySource{}, false
	}
	srcs, err := app.Activities.ListSourcesForActivity(r.Context(), id)
	if err != nil || len(srcs) == 0 {
		return domain.ActivitySource{}, false
	}
	return srcs[0], true
}

// validFieldGroup reports whether fg is a real, user-pinnable field group.
func validFieldGroup(fg domain.FieldGroup) bool {
	for _, g := range domain.AllFieldGroups {
		if g == fg {
			return true
		}
	}
	return false
}

