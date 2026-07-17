package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountShareLinks wires activity share links (multi-user v1).
//
//	POST   /api/activities/{id}/share   owner: mint an unguessable link
//	GET    /api/activities/{id}/shares  owner: list links
//	DELETE /api/shares/{token}          owner: revoke
//	GET    /api/shared/{token}          public: read-only projected activity
func mountShareLinks(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.ShareLinks == nil {
		return
	}

	mux.HandleFunc("POST /api/activities/{id}/share", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		actID, err := domain.ParseUUID[domain.ActivityID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		a, err := app.Activities.GetActivity(r.Context(), actID)
		if err != nil || a.UserID != me {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		token := newShareToken()
		if err := app.ShareLinks.Create(r.Context(), domain.ShareLink{
			Token: token, ActivityID: actID, CreatedBy: me,
		}); err != nil {
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token": token,
			"path":  "/shared/" + token,
		})
	})

	mux.HandleFunc("GET /api/activities/{id}/shares", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		actID, err := domain.ParseUUID[domain.ActivityID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		a, err := app.Activities.GetActivity(r.Context(), actID)
		if err != nil || a.UserID != me {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		links, err := app.ShareLinks.ListForActivity(r.Context(), actID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(links))
		for _, l := range links {
			out = append(out, map[string]any{
				"token":      l.Token,
				"path":       "/shared/" + l.Token,
				"created_at": l.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
				"active":     l.Active(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out})
	})

	mux.HandleFunc("DELETE /api/shares/{token}", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if err := app.ShareLinks.Revoke(r.Context(), r.PathValue("token"), me); err != nil {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Public, session-less read of a shared activity, projected via the 'link'
	// audience (which the owner's policy may further restrict).
	mux.HandleFunc("GET /api/shared/{token}", func(w http.ResponseWriter, r *http.Request) {
		link, err := app.ShareLinks.GetActive(r.Context(), r.PathValue("token"))
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "link not found or revoked", http.StatusNotFound)
				return
			}
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		a, err := app.Activities.GetActivity(r.Context(), link.ActivityID)
		if err != nil {
			http.Error(w, "activity gone", http.StatusNotFound)
			return
		}
		if a.HiddenByAdmin {
			http.Error(w, "unavailable", http.StatusNotFound)
			return
		}
		// A share link is itself a trust signal: resolve as the 'link' audience.
		var viewerPtr *domain.UserID
		if v, ok := optionalSessionUser(r, app); ok {
			viewerPtr = &v
		}
		cats, _ := visibilityFor(r.Context(), app, a.UserID, viewerPtr, a.ID, true)
		isOwner := viewerPtr != nil && *viewerPtr == a.UserID
		if !isOwner && len(cats) == 0 {
			http.Error(w, "not available", http.StatusForbidden)
			return
		}
		act := projectActivityJSON(a, cats, ownerPrivacyZones(r.Context(), app, a.UserID, isOwner))
		owner, _ := app.Users.GetUser(r.Context(), a.UserID)
		writeJSON(w, http.StatusOK, map[string]any{
			"activity": act,
			"owner":    userCardJSON(owner),
			"shared":   true,
		})
	})

	logger.Info("share-link endpoints mounted")
}

// newShareToken returns a 24-byte URL-safe random token (~32 chars).
func newShareToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
