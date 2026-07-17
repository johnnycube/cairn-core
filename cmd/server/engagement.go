package main

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// remoteAuthorCard is the JSON shape for a federated kudos-er / commenter — the
// remote-actor analog of userCardJSON, shared by the kudos + comment reads so
// the two stay consistent.
func remoteAuthorCard(handle, actorID string) map[string]any {
	return map[string]any{
		"display_name": handle, "username": handle,
		"remote": true, "actor": actorID,
	}
}

// truncateUTF8 caps s at maxBytes without splitting a UTF-8 rune, so the result
// stays valid UTF-8 (Postgres rejects invalid byte sequences in a text column).
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// mountEngagement wires kudos + comments (multi-user v1). Both reads and writes
// are gated on the viewer being granted CategorySocial for the activity (or
// being the owner).
//
//	GET    /api/activities/{id}/kudos
//	POST   /api/activities/{id}/kudos
//	DELETE /api/activities/{id}/kudos
//	GET    /api/activities/{id}/comments
//	POST   /api/activities/{id}/comments
//	DELETE /api/comments/{id}
func mountEngagement(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Engagement == nil {
		return
	}

	// socialGate loads the activity, resolves the viewer's categories, and
	// reports whether the viewer may interact socially. Returns the activity
	// and the viewer id on success.
	socialGate := func(w http.ResponseWriter, r *http.Request) (domain.Activity, domain.UserID, bool) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return domain.Activity{}, domain.UserID{}, false
		}
		actID, err := domain.ParseUUID[domain.ActivityID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return domain.Activity{}, domain.UserID{}, false
		}
		a, err := app.Activities.GetActivity(r.Context(), actID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return domain.Activity{}, domain.UserID{}, false
		}
		cats, _ := visibilityFor(r.Context(), app, a.UserID, &me, a.ID, false)
		if !cats.Has(domain.CategorySocial) {
			http.Error(w, "not permitted", http.StatusForbidden)
			return domain.Activity{}, domain.UserID{}, false
		}
		return a, me, true
	}

	mux.HandleFunc("GET /api/activities/{id}/kudos", func(w http.ResponseWriter, r *http.Request) {
		a, me, ok := socialGate(w, r)
		if !ok {
			return
		}
		count, _ := app.Engagement.CountKudos(r.Context(), a.ID)
		mine, _ := app.Engagement.HasKudos(r.Context(), a.ID, me)
		kudosers, _ := app.Engagement.ListKudosers(r.Context(), a.ID, 50)
		people := make([]map[string]any, 0, len(kudosers))
		for _, uid := range kudosers {
			if u, err := app.Users.GetUser(r.Context(), uid); err == nil {
				people = append(people, userCardJSON(u))
			}
		}
		// Fold in federated kudos (remote actors who Liked over ActivityPub).
		remoteCount, _ := app.Engagement.CountRemoteKudos(r.Context(), a.ID)
		if remoteKudosers, err := app.Engagement.ListRemoteKudosers(r.Context(), a.ID, 50); err == nil {
			for _, rk := range remoteKudosers {
				people = append(people, remoteAuthorCard(rk.Handle, rk.ActorID))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"count": count + remoteCount, "has_kudos": mine, "kudosers": people,
		})
	})

	mux.HandleFunc("POST /api/activities/{id}/kudos", func(w http.ResponseWriter, r *http.Request) {
		a, me, ok := socialGate(w, r)
		if !ok {
			return
		}
		if err := app.Engagement.AddKudos(r.Context(), a.ID, me); err != nil {
			http.Error(w, "failed", http.StatusInternalServerError)
			return
		}
		count, _ := app.Engagement.CountKudos(r.Context(), a.ID)
		writeJSON(w, http.StatusOK, map[string]any{"count": count, "has_kudos": true})
	})

	mux.HandleFunc("DELETE /api/activities/{id}/kudos", func(w http.ResponseWriter, r *http.Request) {
		a, me, ok := socialGate(w, r)
		if !ok {
			return
		}
		if err := app.Engagement.RemoveKudos(r.Context(), a.ID, me); err != nil {
			http.Error(w, "failed", http.StatusInternalServerError)
			return
		}
		count, _ := app.Engagement.CountKudos(r.Context(), a.ID)
		writeJSON(w, http.StatusOK, map[string]any{"count": count, "has_kudos": false})
	})

	mux.HandleFunc("GET /api/activities/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := socialGate(w, r)
		if !ok {
			return
		}
		comments, _ := app.Engagement.ListComments(r.Context(), a.ID, 200, 0)
		type tagged struct {
			t time.Time
			m map[string]any
		}
		rows := make([]tagged, 0, len(comments))
		for _, c := range comments {
			card := map[string]any{}
			if u, err := app.Users.GetUser(r.Context(), c.UserID); err == nil {
				card = userCardJSON(u)
			}
			rows = append(rows, tagged{c.CreatedAt, map[string]any{
				"id":         c.ID.String(),
				"body":       c.Body,
				"created_at": c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
				"author":     card,
			}})
		}
		// Merge in federated replies (remote actors via ActivityPub).
		if remote, err := app.Engagement.ListRemoteComments(r.Context(), a.ID, 200); err == nil {
			for _, rc := range remote {
				rows = append(rows, tagged{rc.CreatedAt, map[string]any{
					"id":         rc.ID,
					"body":       rc.Body,
					"created_at": rc.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
					"author":     remoteAuthorCard(rc.Handle, rc.ActorID),
				}})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].t.Before(rows[j].t) })
		out := make([]map[string]any, len(rows))
		for i, row := range rows {
			out[i] = row.m
		}
		writeJSON(w, http.StatusOK, map[string]any{"comments": out})
	})

	mux.HandleFunc("POST /api/activities/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		a, me, ok := socialGate(w, r)
		if !ok {
			return
		}
		var body struct {
			Body string `json:"body"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		text := strings.TrimSpace(body.Body)
		if text == "" {
			http.Error(w, "empty comment", http.StatusBadRequest)
			return
		}
		text = truncateUTF8(text, domain.MaxCommentLength)
		id, err := app.Engagement.AddComment(r.Context(), domain.Comment{
			ActivityID: a.ID, UserID: me, Body: text,
		})
		if err != nil {
			http.Error(w, "failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id.String()})
	})

	mux.HandleFunc("DELETE /api/comments/{id}", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := domain.ParseUUID[domain.CommentID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		// DeleteComment enforces author-or-activity-owner in SQL — a no-op when
		// the requester is neither (returns 204 regardless, no info leak).
		if err := app.Engagement.DeleteComment(r.Context(), id, me); err != nil {
			http.Error(w, "failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	logger.Info("engagement (kudos+comments) endpoints mounted")
}
