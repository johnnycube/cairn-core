package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountSocial wires the follow-graph endpoints (multi-user v1).
//
//	POST   /api/users/{id}/follow      follow the user
//	DELETE /api/users/{id}/follow      unfollow
//	GET    /api/users/{id}/follow-state  { following, followers, following_count }
//	GET    /api/users/{id}/followers   [{id,username,display_name,avatar_url}]
//	GET    /api/users/{id}/following
func mountSocial(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Follows == nil {
		return
	}
	h := &socialAPI{app: app, logger: logger}
	mux.HandleFunc("POST /api/users/{id}/follow", h.follow)
	mux.HandleFunc("DELETE /api/users/{id}/follow", h.unfollow)
	mux.HandleFunc("GET /api/users/{id}/follow-state", h.followState)
	mux.HandleFunc("GET /api/users/{id}/followers", h.listHandler("followers"))
	mux.HandleFunc("GET /api/users/{id}/following", h.listHandler("following"))
	// Following feed: reverse-chronological activities from people you follow.
	mux.HandleFunc("GET /api/feed/following", h.followingFeed)
	// Projected single-activity read for non-owners (followers / public).
	// The owner's own rich view goes through Connect-RPC (owner-only); this is
	// the gated cross-user path that the feed/profile cards link to.
	mux.HandleFunc("GET /api/activities/{id}/projected", h.projected)

	logger.Info("social (follow-graph) endpoints mounted")
}

// socialAPI holds the dependencies of the follow-graph handlers.
type socialAPI struct {
	app    *App
	logger *slog.Logger
}

// meAndTarget resolves the session user and the {id} target or writes an error.
func (h *socialAPI) meAndTarget(w http.ResponseWriter, r *http.Request) (domain.UserID, domain.UserID, bool) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return me, domain.UserID{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return me, domain.UserID{}, false
	}
	return me, domain.UserID(id), true
}

func (h *socialAPI) follow(w http.ResponseWriter, r *http.Request) {
	app := h.app
	me, target, ok := h.meAndTarget(w, r)
	if !ok {
		return
	}
	if me == target {
		http.Error(w, "cannot follow yourself", http.StatusBadRequest)
		return
	}
	if app.Blocks != nil {
		if blocked, _ := app.Blocks.IsBlockedEitherWay(r.Context(), me, target); blocked {
			http.Error(w, "cannot follow this user", http.StatusForbidden)
			return
		}
	}
	if err := app.Follows.Follow(r.Context(), me, target); err != nil {
		h.logger.Error("follow failed", "error", err)
		http.Error(w, "follow failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"following": true})
}

func (h *socialAPI) unfollow(w http.ResponseWriter, r *http.Request) {
	me, target, ok := h.meAndTarget(w, r)
	if !ok {
		return
	}
	if err := h.app.Follows.Unfollow(r.Context(), me, target); err != nil {
		http.Error(w, "unfollow failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"following": false})
}

func (h *socialAPI) followState(w http.ResponseWriter, r *http.Request) {
	me, target, ok := h.meAndTarget(w, r)
	if !ok {
		return
	}
	following, _ := h.app.Follows.IsFollowing(r.Context(), me, target)
	counts, err := h.app.Follows.Counts(r.Context(), target)
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"following":       following,
		"followers_count": counts.Followers,
		"following_count": counts.Following,
		"is_self":         me == target,
	})
}

// listHandler serves the followers ("followers") or following list of a user.
func (h *socialAPI) listHandler(which string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := h.app
		_, target, ok := h.meAndTarget(w, r)
		if !ok {
			return
		}
		limit := parseIntQuery(r, "limit", 50)
		offset := parseIntQuery(r, "offset", 0)
		var ids []domain.UserID
		var err error
		if which == "followers" {
			ids, err = app.Follows.ListFollowers(r.Context(), target, limit, offset)
		} else {
			ids, err = app.Follows.ListFollowing(r.Context(), target, limit, offset)
		}
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			u, err := app.Users.GetUser(r.Context(), id)
			if err != nil {
				h.logger.Warn("follow list: load user failed", "user_id", id, "error", err)
				continue
			}
			out = append(out, userCardJSON(u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	}
}

func (h *socialAPI) followingFeed(w http.ResponseWriter, r *http.Request) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	limit := parseIntQuery(r, "limit", 30)
	if limit > 100 {
		limit = 100
	}
	offset := parseIntQuery(r, "offset", 0)
	acts, err := h.app.Activities.ListFollowingFeed(r.Context(), me, limit, offset)
	if err != nil {
		h.logger.Error("following feed failed", "error", err)
		http.Error(w, "feed failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"activities": h.feedItems(r, me, acts),
		"offset":     offset,
		"limit":      limit,
		// has_more reflects whether the DB returned a full page, independent
		// of how many survived the visibility filter.
		"has_more": len(acts) == limit,
	})
}

// feedItems projects feed rows for the viewer, dropping ones they may not see.
func (h *socialAPI) feedItems(r *http.Request, me domain.UserID, acts []domain.Activity) []map[string]any {
	app := h.app
	// Every row goes through the visibility choke-point (visibilityFor +
	// projectActivityJSON), exactly like the single-activity projected path,
	// so per-field policy + privacy zones are enforced — not just the coarse
	// privacy flag. Owner cards + zones are cached per owner.
	viewerPtr := &me
	owners := map[domain.UserID]map[string]any{}
	zonesByOwner := map[domain.UserID][]domain.PrivacyZone{}
	out := make([]map[string]any, 0, len(acts))
	for _, a := range acts {
		cats := visibilityFor(r.Context(), app, a.UserID, viewerPtr, a.ID, false)
		if len(cats) == 0 {
			continue // blocked, or the owner's policy shares nothing with this viewer
		}
		owner, cached := owners[a.UserID]
		if !cached {
			if u, err := app.Users.GetUser(r.Context(), a.UserID); err == nil {
				owner = userCardJSON(u)
			}
			owners[a.UserID] = owner
			zonesByOwner[a.UserID] = ownerPrivacyZones(r.Context(), app, a.UserID, false)
		}
		item := projectActivityJSON(a, cats, zonesByOwner[a.UserID])
		item["owner"] = owner
		out = append(out, item)
	}
	return out
}

func (h *socialAPI) projected(w http.ResponseWriter, r *http.Request) {
	app := h.app
	actID, err := domain.ParseUUID[domain.ActivityID](r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	a, err := app.Activities.GetActivity(r.Context(), actID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var viewerPtr *domain.UserID
	if v, ok := optionalSessionUser(r, app); ok {
		viewerPtr = &v
	}
	isOwner := viewerPtr != nil && *viewerPtr == a.UserID
	// Non-owners can never see a strictly-private or admin-hidden activity.
	if !isOwner && (a.Privacy == domain.PrivacyPrivate || a.HiddenByAdmin) {
		http.Error(w, "not permitted", http.StatusForbidden)
		return
	}
	cats := visibilityFor(r.Context(), app, a.UserID, viewerPtr, a.ID, false)
	// An empty grant (e.g. a block, or a policy that shares nothing with
	// this audience) means the viewer may not see the activity at all.
	if !isOwner && len(cats) == 0 {
		http.Error(w, "not permitted", http.StatusForbidden)
		return
	}
	owner, _ := app.Users.GetUser(r.Context(), a.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"activity": projectActivityJSON(a, cats, ownerPrivacyZones(r.Context(), app, a.UserID, isOwner)),
		"owner":    userCardJSON(owner),
		"is_owner": isOwner,
	})
}

// userCardJSON is the compact public-facing shape of a user in social lists.
func userCardJSON(u domain.User) map[string]any {
	return map[string]any{
		"id":           u.ID.String(),
		"username":     u.Username,
		"display_name": u.DisplayName,
		"avatar_url":   u.AvatarURL,
	}
}
