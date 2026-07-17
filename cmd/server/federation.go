package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/httpsig"
)

// federation.go is the ActivityPub surface (docs/federation-design.md):
//   - Phase 1: discovery + actor — WebFinger, NodeInfo, the Person actor
//     document, and its followers/following/outbox collections.
//   - Phase 2: the inbox — HTTP-signature-verified Follow / Accept / Create,
//     plus outbound Follow (POST /api/federation/follow).
//   - Phase 3: outbound publishing — fresh public activities are pushed to
//     followers' inboxes (see federation_publish.go), hooked off the ingest
//     pipeline's follow-ups.
//
// Gated twice: the instance flag (CAIRN_FEDERATION_ENABLED, checked by the
// caller before mounting) AND the per-user opt-in (users.federation_enabled).
// A non-opted-in user 404s on every federation route — indistinguishable from
// "no such user".

const apContentType = "application/activity+json"

func mountFederation(mux *http.ServeMux, app *App, cfg *config.Config, logger *slog.Logger) {
	base := strings.TrimRight(cfg.HTTP.PublicBaseURL, "/")
	host := ""
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	actorID := func(username string) string { return base + "/users/" + username }

	// On a trusted private network (LAN self-hosting / the interop harness) the
	// SSRF dial guard would block peers on private IPs — drop it when the
	// operator explicitly opts in.
	if cfg.Federation.AllowPrivateHosts {
		federationClient = &http.Client{Timeout: 15 * time.Second}
		federationScheme = "http" // LAN/test peers serve plain HTTP
		logger.Warn("federation: SSRF dial guard disabled + WebFinger over http (CAIRN_FEDERATION_ALLOW_PRIVATE_HOSTS) — use only on trusted networks")
	}

	// Per-domain inbox rate limiter (DoS guard): ~120 inbound activities/min
	// per remote instance. Reuses the shared in-memory token bucket (which caps
	// its bucket map so forged-domain floods can't grow memory unbounded).
	inboxLimit := newIPRateLimiter(120, nil)

	// resolveActor returns the user iff federation is enabled for them.
	resolveActor := func(ctx context.Context, rawUsername string) (domain.User, bool) {
		u, err := app.Users.GetUserByUsername(ctx, strings.ToLower(strings.TrimSpace(rawUsername)))
		if err != nil {
			return domain.User{}, false
		}
		enabled, err := app.Users.IsFederationEnabled(ctx, u.ID)
		if err != nil || !enabled {
			return domain.User{}, false
		}
		return u, true
	}

	// --- WebFinger: acct:user@host -> actor id -------------------------------
	mux.HandleFunc("GET /.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		username, ok := parseAcct(r.URL.Query().Get("resource"), host)
		if !ok {
			http.Error(w, "bad resource", http.StatusBadRequest)
			return
		}
		u, ok := resolveActor(r.Context(), username)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"subject": "acct:" + u.Username + "@" + host,
			"links": []any{
				map[string]any{"rel": "self", "type": apContentType, "href": actorID(u.Username)},
			},
		})
	})

	// --- NodeInfo: instance metadata discovery -------------------------------
	mux.HandleFunc("GET /.well-known/nodeinfo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"links": []any{map[string]any{
				"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.1",
				"href": base + "/nodeinfo/2.1",
			}},
		})
	})
	mux.HandleFunc("GET /nodeinfo/2.1", func(w http.ResponseWriter, _ *http.Request) {
		version, _, _ := buildInfo()
		writeJSON(w, http.StatusOK, map[string]any{
			"version":           "2.1",
			"software":          map[string]any{"name": "cairn", "version": version},
			"protocols":         []string{"activitypub"},
			"services":          map[string]any{"inbound": []string{}, "outbound": []string{}},
			"openRegistrations": false,
			// User/post counts omitted on purpose (privacy on a personal instance).
			"usage":    map[string]any{"users": map[string]any{}},
			"metadata": map[string]any{"nodeName": "Cairn"},
		})
	})

	// --- Actor document ------------------------------------------------------
	mux.HandleFunc("GET /users/{username}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := resolveActor(r.Context(), r.PathValue("username"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		pubPEM, err := app.FederationKeys.GetOrCreatePublicPEM(r.Context(), u.ID)
		if err != nil {
			logger.Error("federation: key load failed", "user", u.ID, "error", err)
			http.Error(w, "key error", http.StatusInternalServerError)
			return
		}
		id := actorID(u.Username)
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		writeAP(w, map[string]any{
			"@context": []any{
				"https://www.w3.org/ns/activitystreams",
				"https://w3id.org/security/v1",
			},
			"type":              "Person",
			"id":                id,
			"preferredUsername": u.Username,
			"name":              name,
			"url":               base + "/u/" + u.Username,
			"inbox":             id + "/inbox",
			"outbox":            id + "/outbox",
			"followers":         id + "/followers",
			"following":         id + "/following",
			"publicKey": map[string]any{
				"id":           id + "#main-key",
				"owner":        id,
				"publicKeyPem": pubPEM,
			},
		})
	})

	// --- Collections ---------------------------------------------------------
	mux.HandleFunc("GET /users/{username}/followers", func(w http.ResponseWriter, r *http.Request) {
		u, ok := resolveActor(r.Context(), r.PathValue("username"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		counts, _ := app.Follows.Counts(r.Context(), u.ID)
		writeAP(w, orderedCollection(actorID(u.Username)+"/followers", counts.Followers))
	})
	mux.HandleFunc("GET /users/{username}/following", func(w http.ResponseWriter, r *http.Request) {
		u, ok := resolveActor(r.Context(), r.PathValue("username"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		counts, _ := app.Follows.Counts(r.Context(), u.ID)
		writeAP(w, orderedCollection(actorID(u.Username)+"/following", counts.Following))
	})
	// Outbox: a paged OrderedCollection of the user's public activities, each
	// rendered as the same Create we push to followers (Phase 3). The root
	// returns metadata + a `first` page link; `?page=true[&offset=N]` returns
	// an OrderedCollectionPage so a new follower's server can backfill history.
	const outboxPageSize = 20
	mux.HandleFunc("GET /users/{username}/outbox", func(w http.ResponseWriter, r *http.Request) {
		u, ok := resolveActor(r.Context(), r.PathValue("username"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		outboxID := actorID(u.Username) + "/outbox"
		total, _ := app.Activities.CountPublicActivitiesForUser(r.Context(), u.ID)

		if r.URL.Query().Get("page") != "true" {
			writeAP(w, map[string]any{
				"@context":   "https://www.w3.org/ns/activitystreams",
				"type":       "OrderedCollection",
				"id":         outboxID,
				"totalItems": total,
				"first":      outboxID + "?page=true",
			})
			return
		}

		offset := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				offset = n
			}
		}
		acts, _ := app.Activities.ListPublicActivitiesForUser(r.Context(), u.ID, outboxPageSize, offset)
		aid := actorID(u.Username)
		items := make([]any, 0, len(acts))
		for _, a := range acts {
			create := buildActivityCreate(base, aid, aid+"/activities/"+a.ID.String(), a)
			delete(create, "@context") // embedded items inherit the page's context
			items = append(items, create)
		}
		page := map[string]any{
			"@context":     "https://www.w3.org/ns/activitystreams",
			"type":         "OrderedCollectionPage",
			"id":           outboxID + "?page=true&offset=" + strconv.Itoa(offset),
			"partOf":       outboxID,
			"orderedItems": items,
		}
		if offset+len(acts) < total {
			page["next"] = outboxID + "?page=true&offset=" + strconv.Itoa(offset+outboxPageSize)
		}
		writeAP(w, page)
	})

	// --- Inbox: HTTP-signature-verified inbound activities -------------------
	mux.HandleFunc("POST /users/{username}/inbox", func(w http.ResponseWriter, r *http.Request) {
		target, ok := resolveActor(r.Context(), r.PathValue("username"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}

		// Authenticate: fetch the signer's actor (by keyId), verify the signature.
		keyID, ok := httpsig.KeyIDFromRequest(r)
		if !ok {
			http.Error(w, "unsigned request", http.StatusUnauthorized)
			return
		}
		remoteActorID := strings.SplitN(keyID, "#", 2)[0]
		remoteHost := hostOf(remoteActorID)
		// A keyId with no resolvable host can't be blocked or rate-limited by
		// domain, so reject it outright rather than letting it slip past both
		// guards into the expensive fetch+verify path.
		if remoteHost == "" {
			http.Error(w, "bad actor", http.StatusBadRequest)
			return
		}
		// Instance defederation: reject anything signed by a blocked domain
		// before we even fetch the signer.
		if domainBlocked(r.Context(), app, remoteHost) {
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		// DoS guard: bound inbound work per remote domain before the expensive
		// signer fetch + verification.
		if !inboxLimit.allow(remoteHost) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		remoteActor, pub, err := fetchRemoteActor(r.Context(), app, remoteActorID)
		if err != nil {
			logger.Warn("federation: cannot fetch signer", "actor", remoteActorID, "error", err)
			http.Error(w, "cannot verify signer", http.StatusUnauthorized)
			return
		}
		if err := httpsig.Verify(r, body, pub); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var act apActivity
		if err := json.Unmarshal(body, &act); err != nil {
			http.Error(w, "bad activity", http.StatusBadRequest)
			return
		}
		// Drop at-least-once duplicates.
		if act.ID != "" && app.InboxDedup != nil {
			if seen, _ := app.InboxDedup.SeenOrMark(r.Context(), act.ID); seen {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}

		switch act.Type {
		case "Follow":
			// The Follow must target this user; auto-accept (the user opted in).
			if act.objectID() != actorID(target.Username) {
				http.Error(w, "wrong target", http.StatusBadRequest)
				return
			}
			if err := app.FederationFollows.Upsert(r.Context(), domain.FederationFollow{
				LocalUserID: target.ID, RemoteActorID: remoteActorID,
				Direction: domain.FederationFollowInbound, Status: domain.FederationFollowAccepted,
				FollowActivityID: act.ID,
			}); err != nil {
				logger.Error("federation: store inbound follow", "error", err)
			}
			// Reply with a signed Accept, delivered to the follower's inbox.
			go func(actID string, raw []byte, inbox string) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				accept := map[string]any{
					"@context": "https://www.w3.org/ns/activitystreams",
					"id":       actID + "#accepts/" + url.PathEscape(act.ID),
					"type":     "Accept",
					"actor":    actID,
					"object":   json.RawMessage(raw),
				}
				if err := deliverActivity(ctx, app, target.ID, actID, inbox, accept); err != nil {
					logger.Warn("federation: deliver Accept failed", "to", inbox, "error", err)
				}
			}(actorID(target.Username), body, remoteActor.Inbox)
			w.WriteHeader(http.StatusAccepted)

		case "Accept":
			// A remote accepted OUR outbound Follow → mark it accepted.
			if err := app.FederationFollows.MarkAccepted(r.Context(), target.ID, remoteActorID, domain.FederationFollowOutbound); err != nil {
				logger.Warn("federation: mark outbound accepted", "error", err)
			}
			w.WriteHeader(http.StatusAccepted)

		case "Create":
			// A reply (Note with inReplyTo a local public activity) → comment.
			// Otherwise a workout from a followed actor → home-feed item.
			if noteID, inReplyTo, body, isReply := remoteComment(act.Object); isReply {
				if aid, ok := localActivityIDFromAPURL(inReplyTo, host); ok {
					if act2, err := app.Activities.GetActivity(r.Context(), aid); err == nil &&
						act2.UserID == target.ID && act2.Privacy == domain.PrivacyPublic && !act2.HiddenByAdmin {
						body = truncateUTF8(body, domain.MaxCommentLength)
						if err := app.Engagement.AddRemoteComment(r.Context(), aid, remoteActorID, noteID, body); err != nil {
							logger.Warn("federation: add remote comment", "error", err)
						}
					}
				}
			} else {
				// Only ingest workouts from actors this user actually follows
				// (anti-spam: strangers are dropped).
				following, _ := app.FederationFollows.Exists(r.Context(), target.ID, remoteActorID, domain.FederationFollowOutbound)
				if following {
					if item, ok := feedItemFromObject(target.ID, remoteActorID, act.Object); ok {
						if err := app.FederationFeed.Insert(r.Context(), item); err != nil {
							logger.Warn("federation: store feed item", "error", err)
						}
					}
				}
			}
			w.WriteHeader(http.StatusAccepted)

		case "Like":
			// A remote actor likes one of this user's public activities → record
			// a federated kudos. Scoped to the inbox owner's own public,
			// non-hidden activity (no remote likes on private workouts).
			if aid, ok := localActivityIDFromAPURL(act.objectID(), host); ok {
				if liked, err := app.Activities.GetActivity(r.Context(), aid); err == nil &&
					liked.UserID == target.ID && liked.Privacy == domain.PrivacyPublic && !liked.HiddenByAdmin {
					if err := app.Engagement.AddRemoteKudos(r.Context(), aid, remoteActorID, act.ID); err != nil {
						logger.Warn("federation: add remote kudos", "error", err)
					}
				}
			}
			w.WriteHeader(http.StatusAccepted)

		case "Undo":
			// Undo{Follow}: the remote stops following our user → drop the
			// inbound edge so the delivery scheduler stops fanning out to them.
			switch act.objectType() {
			case "Follow":
				if act.undoneFollowTarget() == actorID(target.Username) {
					if err := app.FederationFollows.Delete(r.Context(), target.ID, remoteActorID, domain.FederationFollowInbound); err != nil {
						logger.Warn("federation: undo inbound follow", "error", err)
					}
				}
			case "Like":
				// Undo{Like}: the remote un-likes → remove the federated kudos.
				if aid, ok := localActivityIDFromAPURL(act.undoneObject("Like"), host); ok {
					if err := app.Engagement.RemoveRemoteKudos(r.Context(), aid, remoteActorID); err != nil {
						logger.Warn("federation: undo remote kudos", "error", err)
					}
				}
			}
			w.WriteHeader(http.StatusAccepted)

		case "Delete":
			// A remote deleting its own object/actor. Scoped to the signer so a
			// remote can only remove its own content from this user's view.
			objID := act.objectID()
			switch {
			case objID == "":
				// nothing addressable
			case objID == remoteActorID:
				// Actor self-Delete (leaving the network): drop their feed items
				// and both follow edges with this user.
				if err := app.FederationFeed.DeleteAllFromActor(r.Context(), target.ID, remoteActorID); err != nil {
					logger.Warn("federation: delete actor feed items", "error", err)
				}
				_ = app.Engagement.DeleteRemoteKudosFromActor(r.Context(), target.ID, remoteActorID)
				_ = app.Engagement.DeleteRemoteCommentsFromActor(r.Context(), target.ID, remoteActorID)
				_ = app.FederationFollows.Delete(r.Context(), target.ID, remoteActorID, domain.FederationFollowInbound)
				_ = app.FederationFollows.Delete(r.Context(), target.ID, remoteActorID, domain.FederationFollowOutbound)
			default:
				// Delete{object}: remove that object from this user's view — it
				// may be a feed activity or a comment Note (both scoped to the
				// signer; whichever matches is removed, the other is a no-op).
				if err := app.FederationFeed.DeleteItem(r.Context(), target.ID, remoteActorID, objID); err != nil {
					logger.Warn("federation: delete feed item", "error", err)
				}
				if err := app.Engagement.DeleteRemoteComment(r.Context(), remoteActorID, objID); err != nil {
					logger.Warn("federation: delete remote comment", "error", err)
				}
			}
			w.WriteHeader(http.StatusAccepted)

		default:
			// Like / Announce / etc. — accepted, handled in later phases.
			w.WriteHeader(http.StatusAccepted)
		}
	})

	// --- Per-user opt-in toggle (session-gated) ------------------------------
	mux.HandleFunc("PUT /api/profile/federation", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := app.Users.SetFederationEnabled(r.Context(), uid, body.Enabled); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		// Generate the keypair eagerly on enable so the actor is immediately complete.
		if body.Enabled {
			if _, err := app.FederationKeys.GetOrCreatePublicPEM(r.Context(), uid); err != nil {
				logger.Error("federation: key gen on enable failed", "user", uid, "error", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled})
	})

	// --- Follow a remote actor (session-gated) -------------------------------
	// Resolves a user@host handle via WebFinger and sends a signed Follow so the
	// remote actor's future activities are delivered to this user's inbox.
	mux.HandleFunc("POST /api/federation/follow", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Handle string `json:"handle"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Handle) == "" {
			http.Error(w, "handle required", http.StatusBadRequest)
			return
		}
		if enabled, _ := app.Users.IsFederationEnabled(r.Context(), uid); !enabled {
			http.Error(w, "enable federation on your profile first", http.StatusConflict)
			return
		}
		me, err := app.Users.GetUser(r.Context(), uid)
		if err != nil {
			http.Error(w, "user error", http.StatusInternalServerError)
			return
		}
		actorURL, err := resolveRemoteHandle(r.Context(), body.Handle)
		if err != nil {
			http.Error(w, "could not resolve "+body.Handle, http.StatusNotFound)
			return
		}
		remoteActor, _, err := fetchRemoteActor(r.Context(), app, actorURL)
		if err != nil {
			http.Error(w, "could not fetch remote actor", http.StatusBadGateway)
			return
		}
		_ = app.FederationFollows.Upsert(r.Context(), domain.FederationFollow{
			LocalUserID: uid, RemoteActorID: actorURL,
			Direction: domain.FederationFollowOutbound, Status: domain.FederationFollowPending,
		})
		myActor := actorID(me.Username)
		follow := map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       myActor + "#follows/" + url.PathEscape(actorURL),
			"type":     "Follow", "actor": myActor, "object": actorURL,
		}
		if err := deliverActivity(r.Context(), app, uid, myActor, remoteActor.Inbox, follow); err != nil {
			logger.Warn("federation: deliver Follow failed", "to", remoteActor.Inbox, "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"following": actorURL, "pending": true})
	})

	// --- Admin: instance defederation blocklist (session + admin gated) ------
	mux.HandleFunc("GET /api/admin/federation/blocks", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		list, _ := app.FederationBlocks.List(r.Context())
		out := make([]map[string]any, 0, len(list))
		for _, b := range list {
			out = append(out, map[string]any{
				"domain": b.Domain, "reason": b.Reason,
				"created_at": b.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"blocked": out})
	})
	mux.HandleFunc("POST /api/admin/federation/blocks", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Domain string `json:"domain"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Domain) == "" {
			http.Error(w, "domain required", http.StatusBadRequest)
			return
		}
		if err := app.FederationBlocks.Block(r.Context(), body.Domain, body.Reason); err != nil {
			http.Error(w, "block failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"domain": strings.ToLower(strings.TrimSpace(body.Domain)), "blocked": true,
		})
	})
	mux.HandleFunc("DELETE /api/admin/federation/blocks/{domain}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		if err := app.FederationBlocks.Unblock(r.Context(), r.PathValue("domain")); err != nil {
			http.Error(w, "unblock failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	logger.Info("federation (ActivityPub phases 1–3) endpoints mounted",
		"host", host, "paths", []string{"/.well-known/webfinger", "/.well-known/nodeinfo", "/users/{username}"})
}

// orderedCollection is a minimal AS2 OrderedCollection carrying a totalItems
// count without inlining items (a first-page link comes with paging, later).
func orderedCollection(id string, total int) map[string]any {
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"type":         "OrderedCollection",
		"id":           id,
		"totalItems":   total,
		"orderedItems": []any{},
	}
}

// parseAcct extracts the username from "acct:username@host", requiring the host
// to match this instance. Returns ok=false on any mismatch.
func parseAcct(resource, host string) (string, bool) {
	resource = strings.TrimSpace(resource)
	resource = strings.TrimPrefix(resource, "acct:")
	at := strings.LastIndexByte(resource, '@')
	if at <= 0 {
		return "", false
	}
	user, dom := resource[:at], resource[at+1:]
	if user == "" || !strings.EqualFold(dom, host) {
		return "", false
	}
	return user, true
}

// writeAP writes an ActivityPub document with the correct content type.
func writeAP(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", apContentType)
	_ = json.NewEncoder(w).Encode(body)
}
