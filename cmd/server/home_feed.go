package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountHomeFeed serves the logged-in landing feed: the user's local
// following-feed (projected through the visibility choke-point) merged with
// federated activities received from remote actors they follow, newest first.
func mountHomeFeed(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/feed/home", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		limit := parseIntQuery(r, "limit", 30)
		if limit > 100 {
			limit = 100
		}
		offset := parseIntQuery(r, "offset", 0)

		type entry struct {
			t time.Time
			m map[string]any
		}
		var entries []entry

		// The user's OWN activities (full data — they own them).
		var meCard map[string]any
		if u, err := app.Users.GetUser(r.Context(), me); err == nil {
			meCard = userCardJSON(u)
		}
		own, _ := app.Activities.ListRecentActivitiesForUser(r.Context(), me, limit)
		for _, a := range own {
			entries = append(entries, entry{a.StartTime, map[string]any{
				"source":             "self",
				"id":                 a.ID.String(),
				"title":              a.Title,
				"type":               string(a.Type),
				"start_time":         a.StartTime.UTC().Format(time.RFC3339),
				"distance_m":         a.Summary.DistanceM,
				"elevation_gain_m":   a.Summary.ElevationGainM,
				"elapsed_duration_s": int64(a.ElapsedDuration.Seconds()),
				"start_place":        a.StartPlace,
				"map_image_url":      "/api/activities/" + a.ID.String() + "/map.png",
				"owner":              meCard,
			}})
		}

		// Friends on the same server — the local following feed, projected
		// through the visibility choke-point.
		acts, _ := app.Activities.ListFollowingFeed(r.Context(), me, limit, offset)
		viewerPtr := &me
		owners := map[domain.UserID]map[string]any{}
		zonesByOwner := map[domain.UserID][]domain.PrivacyZone{}
		for _, a := range acts {
			cats, _ := visibilityFor(r.Context(), app, a.UserID, viewerPtr, a.ID, false)
			if len(cats) == 0 {
				continue
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
			item["source"] = "following"
			item["owner"] = owner
			entries = append(entries, entry{a.StartTime, item})
		}

		// Federated activities received from followed remote actors. Gated on
		// the instance flag, not just the wired repo: with federation switched
		// off, items received while it was on must stay dormant too.
		if app.FederationEnabled && app.FederationFeed != nil {
			fitems, _ := app.FederationFeed.ListForUser(r.Context(), me, limit, offset)
			for _, it := range fitems {
				m := map[string]any{
					"source":           "federated",
					"id":               it.ActivityAPID,
					"title":            it.Name,
					"type":             it.Sport,
					"start_time":       it.Published.UTC().Format(time.RFC3339),
					"summary":          it.Summary,
					"url":              it.URL,
					"map_image_url":    it.ImageURL,
					"distance_m":       it.DistanceM,
					"elevation_gain_m": it.ElevationM,
					"owner": map[string]any{
						"display_name": remoteHandle(it.ActorID),
						"remote":       true,
						"actor":        it.ActorID,
					},
				}
				if it.DurationS != nil {
					m["elapsed_duration_s"] = *it.DurationS
				}
				entries = append(entries, entry{it.Published, m})
			}
		}

		sort.SliceStable(entries, func(i, j int) bool { return entries[i].t.After(entries[j].t) })
		if len(entries) > limit {
			entries = entries[:limit]
		}
		out := make([]map[string]any, len(entries))
		for i, e := range entries {
			out[i] = e.m
		}
		writeJSON(w, http.StatusOK, map[string]any{"activities": out})
	})
	logger.Info("home feed endpoint mounted", "path", "/api/feed/home")
}

// remoteHandle renders an actor URL as @user@host for display.
func remoteHandle(actorID string) string {
	if u, err := url.Parse(actorID); err == nil && u.Host != "" {
		return "@" + path.Base(u.Path) + "@" + u.Host
	}
	return actorID
}
