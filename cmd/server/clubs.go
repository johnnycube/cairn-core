package main

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/johnnycube/cairn-core/internal/domain"
)

var clubSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a club name into a URL-safe slug.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = clubSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// mountClubs wires clubs / groups / teams (multi-user v1).
//
//	POST   /api/clubs                  create a club
//	GET    /api/clubs                  list public + my clubs
//	GET    /api/clubs/{slug}           club detail (+ my membership)
//	POST   /api/clubs/{slug}/join      join (public clubs)
//	DELETE /api/clubs/{slug}/leave     leave (non-owner)
//	GET    /api/clubs/{slug}/members   member list
//	GET    /api/clubs/{slug}/feed      member activity feed
func mountClubs(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Clubs == nil {
		return
	}

	// resolveClub loads a club by slug, returning false (404 written) on miss.
	resolveClub := func(w http.ResponseWriter, r *http.Request) (domain.Club, bool) {
		c, err := app.Clubs.GetBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			http.Error(w, "club not found", http.StatusNotFound)
			return domain.Club{}, false
		}
		return c, true
	}

	mux.HandleFunc("POST /api/clubs", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			IsPublic    *bool  `json:"is_public"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if len(name) > domain.MaxClubNameLength {
			name = name[:domain.MaxClubNameLength]
		}
		slug := slugify(name)
		if slug == "" {
			http.Error(w, "name must contain letters or digits", http.StatusBadRequest)
			return
		}
		desc := strings.TrimSpace(body.Description)
		if len(desc) > domain.MaxClubDescriptionLength {
			desc = desc[:domain.MaxClubDescriptionLength]
		}
		isPublic := true
		if body.IsPublic != nil {
			isPublic = *body.IsPublic
		}
		// Disambiguate slug collisions by suffixing the user's short id.
		if _, err := app.Clubs.GetBySlug(r.Context(), slug); err == nil {
			slug = slug + "-" + me.String()[:8]
		}
		id, err := app.Clubs.Create(r.Context(), domain.Club{
			Slug: slug, Name: name, Description: desc, OwnerID: me, IsPublic: isPublic,
		})
		if err != nil {
			logger.Error("create club failed", "error", err)
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id.String(), "slug": slug})
	})

	mux.HandleFunc("GET /api/clubs", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		clubs, err := app.Clubs.List(r.Context(), me, parseIntQuery(r, "limit", 50), parseIntQuery(r, "offset", 0))
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(clubs))
		for _, c := range clubs {
			n, _ := app.Clubs.CountMembers(r.Context(), c.ID)
			out = append(out, clubCardJSON(c, n))
		}
		writeJSON(w, http.StatusOK, map[string]any{"clubs": out})
	})

	mux.HandleFunc("GET /api/clubs/{slug}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := resolveClub(w, r)
		if !ok {
			return
		}
		me, hasMe := resolveSessionUser(r, app)
		role := domain.ClubMemberRole("")
		isMember := false
		if hasMe {
			if rr, member, _ := app.Clubs.MemberRole(r.Context(), c.ID, me); member {
				role = rr
				isMember = true
			}
		}
		// A private club is only visible to its members.
		if !c.IsPublic && !isMember {
			http.Error(w, "this club is private", http.StatusForbidden)
			return
		}
		n, _ := app.Clubs.CountMembers(r.Context(), c.ID)
		body := clubCardJSON(c, n)
		body["description"] = c.Description
		body["is_member"] = isMember
		body["my_role"] = string(role)
		writeJSON(w, http.StatusOK, body)
	})

	mux.HandleFunc("POST /api/clubs/{slug}/join", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		c, ok := resolveClub(w, r)
		if !ok {
			return
		}
		if !c.IsPublic {
			http.Error(w, "this club is invite-only", http.StatusForbidden)
			return
		}
		if err := app.Clubs.AddMember(r.Context(), c.ID, me, domain.ClubRoleMember); err != nil {
			http.Error(w, "join failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"is_member": true})
	})

	mux.HandleFunc("DELETE /api/clubs/{slug}/leave", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		c, ok := resolveClub(w, r)
		if !ok {
			return
		}
		if c.OwnerID == me {
			http.Error(w, "the owner cannot leave their own club", http.StatusBadRequest)
			return
		}
		if err := app.Clubs.RemoveMember(r.Context(), c.ID, me); err != nil {
			http.Error(w, "leave failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"is_member": false})
	})

	mux.HandleFunc("GET /api/clubs/{slug}/members", func(w http.ResponseWriter, r *http.Request) {
		c, ok := resolveClub(w, r)
		if !ok {
			return
		}
		me, hasMe := resolveSessionUser(r, app)
		if !c.IsPublic {
			member := false
			if hasMe {
				_, member, _ = app.Clubs.MemberRole(r.Context(), c.ID, me)
			}
			if !member {
				http.Error(w, "private club", http.StatusForbidden)
				return
			}
		}
		members, err := app.Clubs.ListMembers(r.Context(), c.ID, 200, 0)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(members))
		for _, m := range members {
			card := map[string]any{"role": string(m.Role)}
			if u, err := app.Users.GetUser(r.Context(), m.UserID); err == nil {
				for k, v := range userCardJSON(u) {
					card[k] = v
				}
			}
			out = append(out, card)
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": out})
	})

	mux.HandleFunc("GET /api/clubs/{slug}/feed", func(w http.ResponseWriter, r *http.Request) {
		c, ok := resolveClub(w, r)
		if !ok {
			return
		}
		me, hasMe := resolveSessionUser(r, app)
		if !c.IsPublic {
			member := false
			if hasMe {
				_, member, _ = app.Clubs.MemberRole(r.Context(), c.ID, me)
			}
			if !member {
				http.Error(w, "private club", http.StatusForbidden)
				return
			}
		}
		acts, err := app.Clubs.ListClubFeed(r.Context(), c.ID, parseIntQuery(r, "limit", 30), parseIntQuery(r, "offset", 0))
		if err != nil {
			http.Error(w, "feed failed", http.StatusInternalServerError)
			return
		}
		// Route every row through the visibility choke-point (club membership is
		// not itself an audience — members see each other at the public/follower
		// tier the owner's policy grants). viewerPtr is nil for anonymous viewers
		// of a public club.
		var viewerPtr *domain.UserID
		if hasMe {
			viewerPtr = &me
		}
		owners := map[domain.UserID]map[string]any{}
		zonesByOwner := map[domain.UserID][]domain.PrivacyZone{}
		out := make([]map[string]any, 0, len(acts))
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
				isOwner := viewerPtr != nil && *viewerPtr == a.UserID
				zonesByOwner[a.UserID] = ownerPrivacyZones(r.Context(), app, a.UserID, isOwner)
			}
			item := projectActivityJSON(a, cats, zonesByOwner[a.UserID])
			item["owner"] = owner
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{"activities": out})
	})

	logger.Info("club endpoints mounted")
}

func clubCardJSON(c domain.Club, memberCount int) map[string]any {
	return map[string]any{
		"id":           c.ID.String(),
		"slug":         c.Slug,
		"name":         c.Name,
		"is_public":    c.IsPublic,
		"member_count": memberCount,
		"created_at":   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}
