package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountProfiles wires public athlete profiles + the owner's profile/visibility
// settings (multi-user v1).
//
//	GET    /api/profiles/{username}        public profile (session optional)
//	GET    /api/profile/settings           owner: profile + visibility settings
//	PUT    /api/profile/public             owner: toggle profile_is_public
//	GET/PUT /api/profile/visibility        owner: default visibility policy
//	GET/POST/DELETE /api/profile/privacy-zones  owner: privacy zones
func mountProfiles(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Users == nil {
		return
	}

	mux.HandleFunc("GET /api/profiles/{username}", func(w http.ResponseWriter, r *http.Request) {
		username := strings.ToLower(strings.TrimSpace(r.PathValue("username")))
		u, err := app.Users.GetUserByUsername(r.Context(), username)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		viewer, hasViewer := optionalSessionUser(r, app)
		isOwner := hasViewer && viewer == u.ID
		// A block (either direction) makes the profile invisible to the viewer.
		if hasViewer && !isOwner && app.Blocks != nil {
			if blocked, _ := app.Blocks.IsBlockedEitherWay(r.Context(), viewer, u.ID); blocked {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
		}
		isFollower := false
		if hasViewer && !isOwner && app.Follows != nil {
			isFollower, _ = app.Follows.IsFollowing(r.Context(), viewer, u.ID)
		}
		// Gate: a non-public profile is only visible to the owner or a follower.
		if !u.ProfileIsPublic && !isOwner && !isFollower {
			http.Error(w, "this profile is private", http.StatusForbidden)
			return
		}

		var counts domain.FollowCounts
		if app.Follows != nil {
			counts, _ = app.Follows.Counts(r.Context(), u.ID)
		}
		following := false
		if hasViewer && !isOwner && app.Follows != nil {
			following, _ = app.Follows.IsFollowing(r.Context(), viewer, u.ID)
		}

		// Recent activities, projected. The owner sees all; others see only
		// non-private activities, each gated through the visibility model.
		recent, _ := app.Activities.ListRecentActivitiesForUser(r.Context(), u.ID, 20)
		acts := make([]map[string]any, 0, len(recent))
		ownerZones := ownerPrivacyZones(r.Context(), app, u.ID, isOwner)
		for _, a := range recent {
			if !isOwner && (a.Privacy == domain.PrivacyPrivate || a.HiddenByAdmin) {
				continue
			}
			var vptr *domain.UserID
			if hasViewer {
				vptr = &viewer
			}
			cats, _ := visibilityFor(r.Context(), app, u.ID, vptr, a.ID, false)
			if !isOwner && len(cats) == 0 {
				continue
			}
			acts = append(acts, projectActivityJSON(a, cats, ownerZones))
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"user":            userCardJSON(u),
			"is_public":       u.ProfileIsPublic,
			"is_self":         isOwner,
			"is_following":    following,
			"followers_count": counts.Followers,
			"following_count": counts.Following,
			"activities":      acts,
		})
	})

	mux.HandleFunc("PUT /api/profile/public", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			Public bool `json:"public"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := app.Users.SetProfilePublic(r.Context(), me, body.Public); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"public": body.Public})
	})

	if app.Visibility != nil {
		mountVisibilitySettings(mux, app, logger)
	}

	logger.Info("profile endpoints mounted")
}

// mountVisibilitySettings wires the owner's default-visibility-policy editor
// and privacy-zone CRUD (#83).
func mountVisibilitySettings(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/profile/visibility", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		p, err := app.Visibility.GetUserPolicy(r.Context(), me)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		if p.ByAudience == nil {
			p = domain.DefaultVisibilityPolicy()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"policy":         domain.MarshalPolicy(p),
			"all_categories": categoryStrings(),
			"all_audiences":  []string{"public", "followers", "link"},
		})
	})

	mux.HandleFunc("PUT /api/profile/visibility", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			Policy map[string][]string `json:"policy"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		p := domain.UnmarshalPolicy(body.Policy)
		if err := app.Visibility.SetUserPolicy(r.Context(), me, p); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"policy": domain.MarshalPolicy(p)})
	})

	mux.HandleFunc("GET /api/profile/privacy-zones", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		zones, err := app.Visibility.ListPrivacyZones(r.Context(), me)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(zones))
		for _, z := range zones {
			out = append(out, map[string]any{
				"id": z.ID.String(), "label": z.Label,
				"lat": z.Lat, "lng": z.Lng, "radius_m": z.RadiusM,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"zones": out})
	})

	mux.HandleFunc("POST /api/profile/privacy-zones", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			Label   string  `json:"label"`
			Lat     float64 `json:"lat"`
			Lng     float64 `json:"lng"`
			RadiusM float64 `json:"radius_m"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body.RadiusM <= 0 {
			body.RadiusM = 200
		}
		id, err := app.Visibility.AddPrivacyZone(r.Context(), domain.PrivacyZone{
			UserID: me, Label: body.Label, Lat: body.Lat, Lng: body.Lng, RadiusM: body.RadiusM,
		})
		if err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id.String()})
	})

	mux.HandleFunc("DELETE /api/profile/privacy-zones/{id}", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := domain.ParseUUID[domain.PrivacyZoneID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := app.Visibility.DeletePrivacyZone(r.Context(), id, me); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func categoryStrings() []string {
	out := make([]string, len(domain.AllCategories))
	for i, c := range domain.AllCategories {
		out[i] = string(c)
	}
	return out
}
