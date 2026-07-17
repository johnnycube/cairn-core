package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountReviewQueue surfaces the confidence-band review queue: activities the
// matcher merged with only medium confidence, for the user to confirm.
//
//	GET  /api/review-queue
//	POST /api/activities/{id}/reviewed   clear the flag
func mountReviewQueue(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/review-queue", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		items, err := app.Activities.ListActivitiesNeedingReview(r.Context(), userID)
		if err != nil {
			logger.Error("review queue list failed", "user_id", userID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"id":               it.ID.String(),
				"title":            it.Title,
				"type":             string(it.Type),
				"start_time":       it.StartTime.UTC(),
				"match_confidence": it.MatchConfidence,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	})

	mux.HandleFunc("POST /api/activities/{id}/reviewed", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		aUUID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		// Ownership check.
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(aUUID))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := app.Activities.ClearNeedsReview(r.Context(), act.ID); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}
