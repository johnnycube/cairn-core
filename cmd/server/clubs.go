package main

import (
	"context"
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

// clubsAPI holds the dependencies of the club endpoints.
type clubsAPI struct {
	app    *App
	logger *slog.Logger
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
	h := &clubsAPI{app: app, logger: logger}
	mux.HandleFunc("POST /api/clubs", h.create)
	mux.HandleFunc("GET /api/clubs", h.list)
	mux.HandleFunc("GET /api/clubs/{slug}", h.detail)
	mux.HandleFunc("POST /api/clubs/{slug}/join", h.join)
	mux.HandleFunc("DELETE /api/clubs/{slug}/leave", h.leave)
	mux.HandleFunc("GET /api/clubs/{slug}/members", h.members)
	mux.HandleFunc("GET /api/clubs/{slug}/feed", h.feed)

	logger.Info("club endpoints mounted")
}

// resolveClub loads a club by slug, returning false (404 written) on miss.
func (h *clubsAPI) resolveClub(w http.ResponseWriter, r *http.Request) (domain.Club, bool) {
	c, err := h.app.Clubs.GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		http.Error(w, "club not found", http.StatusNotFound)
		return domain.Club{}, false
	}
	return c, true
}

// clubsCreateBody is the POST /api/clubs request body.
type clubsCreateBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPublic    *bool  `json:"is_public"`
}

// clubsNormalizeCreate trims and caps the create body; returns an error message on invalid input.
func clubsNormalizeCreate(body clubsCreateBody) (name, slug, desc string, isPublic bool, errMsg string) {
	name = strings.TrimSpace(body.Name)
	if name == "" {
		return "", "", "", false, "name required"
	}
	if len(name) > domain.MaxClubNameLength {
		name = name[:domain.MaxClubNameLength]
	}
	slug = slugify(name)
	if slug == "" {
		return "", "", "", false, "name must contain letters or digits"
	}
	desc = strings.TrimSpace(body.Description)
	if len(desc) > domain.MaxClubDescriptionLength {
		desc = desc[:domain.MaxClubDescriptionLength]
	}
	isPublic = true
	if body.IsPublic != nil {
		isPublic = *body.IsPublic
	}
	return name, slug, desc, isPublic, ""
}

func (h *clubsAPI) create(w http.ResponseWriter, r *http.Request) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	var body clubsCreateBody
	if err := decodeJSONBody(r, &body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	name, slug, desc, isPublic, errMsg := clubsNormalizeCreate(body)
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}
	// Disambiguate slug collisions by suffixing the user's short id.
	if _, err := h.app.Clubs.GetBySlug(r.Context(), slug); err == nil {
		slug = slug + "-" + me.String()[:8]
	}
	id, err := h.app.Clubs.Create(r.Context(), domain.Club{
		Slug: slug, Name: name, Description: desc, OwnerID: me, IsPublic: isPublic,
	})
	if err != nil {
		h.logger.Error("create club failed", "error", err)
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id.String(), "slug": slug})
}

func (h *clubsAPI) list(w http.ResponseWriter, r *http.Request) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	clubs, err := h.app.Clubs.List(r.Context(), me, parseIntQuery(r, "limit", 50), parseIntQuery(r, "offset", 0))
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(clubs))
	for _, c := range clubs {
		n, _ := h.app.Clubs.CountMembers(r.Context(), c.ID)
		out = append(out, clubCardJSON(c, n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clubs": out})
}

func (h *clubsAPI) detail(w http.ResponseWriter, r *http.Request) {
	c, ok := h.resolveClub(w, r)
	if !ok {
		return
	}
	me, hasMe := resolveSessionUser(r, h.app)
	role := domain.ClubMemberRole("")
	isMember := false
	if hasMe {
		if rr, member, _ := h.app.Clubs.MemberRole(r.Context(), c.ID, me); member {
			role = rr
			isMember = true
		}
	}
	// A private club is only visible to its members.
	if !c.IsPublic && !isMember {
		http.Error(w, "this club is private", http.StatusForbidden)
		return
	}
	n, _ := h.app.Clubs.CountMembers(r.Context(), c.ID)
	body := clubCardJSON(c, n)
	body["description"] = c.Description
	body["is_member"] = isMember
	body["my_role"] = string(role)
	writeJSON(w, http.StatusOK, body)
}

func (h *clubsAPI) join(w http.ResponseWriter, r *http.Request) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	c, ok := h.resolveClub(w, r)
	if !ok {
		return
	}
	if !c.IsPublic {
		http.Error(w, "this club is invite-only", http.StatusForbidden)
		return
	}
	if err := h.app.Clubs.AddMember(r.Context(), c.ID, me, domain.ClubRoleMember); err != nil {
		http.Error(w, "join failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"is_member": true})
}

func (h *clubsAPI) leave(w http.ResponseWriter, r *http.Request) {
	me, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	c, ok := h.resolveClub(w, r)
	if !ok {
		return
	}
	if c.OwnerID == me {
		http.Error(w, "the owner cannot leave their own club", http.StatusBadRequest)
		return
	}
	if err := h.app.Clubs.RemoveMember(r.Context(), c.ID, me); err != nil {
		http.Error(w, "leave failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"is_member": false})
}

// resolveReadableClub loads the club and enforces membership for private clubs
// (403 written on denial). Returns the session user when present.
func (h *clubsAPI) resolveReadableClub(w http.ResponseWriter, r *http.Request) (domain.Club, domain.UserID, bool, bool) {
	c, ok := h.resolveClub(w, r)
	if !ok {
		return domain.Club{}, domain.UserID{}, false, false
	}
	me, hasMe := resolveSessionUser(r, h.app)
	if !c.IsPublic {
		member := false
		if hasMe {
			_, member, _ = h.app.Clubs.MemberRole(r.Context(), c.ID, me)
		}
		if !member {
			http.Error(w, "private club", http.StatusForbidden)
			return domain.Club{}, domain.UserID{}, false, false
		}
	}
	return c, me, hasMe, true
}

func (h *clubsAPI) members(w http.ResponseWriter, r *http.Request) {
	c, _, _, ok := h.resolveReadableClub(w, r)
	if !ok {
		return
	}
	members, err := h.app.Clubs.ListMembers(r.Context(), c.ID, 200, 0)
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		card := map[string]any{"role": string(m.Role)}
		if u, err := h.app.Users.GetUser(r.Context(), m.UserID); err == nil {
			for k, v := range userCardJSON(u) {
				card[k] = v
			}
		}
		out = append(out, card)
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (h *clubsAPI) feed(w http.ResponseWriter, r *http.Request) {
	c, me, hasMe, ok := h.resolveReadableClub(w, r)
	if !ok {
		return
	}
	acts, err := h.app.Clubs.ListClubFeed(r.Context(), c.ID, parseIntQuery(r, "limit", 30), parseIntQuery(r, "offset", 0))
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
	writeJSON(w, http.StatusOK, map[string]any{"activities": h.projectFeed(r.Context(), acts, viewerPtr)})
}

// projectFeed filters acts by visibility and attaches owner cards.
func (h *clubsAPI) projectFeed(ctx context.Context, acts []domain.Activity, viewerPtr *domain.UserID) []map[string]any {
	owners := map[domain.UserID]map[string]any{}
	zonesByOwner := map[domain.UserID][]domain.PrivacyZone{}
	out := make([]map[string]any, 0, len(acts))
	for _, a := range acts {
		cats := visibilityFor(ctx, h.app, a.UserID, viewerPtr, a.ID, false)
		if len(cats) == 0 {
			continue
		}
		owner, cached := owners[a.UserID]
		if !cached {
			if u, err := h.app.Users.GetUser(ctx, a.UserID); err == nil {
				owner = userCardJSON(u)
			}
			owners[a.UserID] = owner
			isOwner := viewerPtr != nil && *viewerPtr == a.UserID
			zonesByOwner[a.UserID] = ownerPrivacyZones(ctx, h.app, a.UserID, isOwner)
		}
		item := projectActivityJSON(a, cats, zonesByOwner[a.UserID])
		item["owner"] = owner
		out = append(out, item)
	}
	return out
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
