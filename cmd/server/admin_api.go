package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/domain/capability"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/enrollment"
)

// admin_api exposes session-authenticated, admin-gated JSON endpoints for the
// admin UI (distinct from the bearer-token /admin/* smoketest layer). The SPA
// is logged in via the session cookie, so these check the session user's role.
//
//	GET  /api/admin/worker-enrollments
//	POST /api/admin/worker-enrollments          { provider, expires_in_hours, worker_name_pattern? }
//	POST /api/admin/worker-enrollments/{id}/revoke   { reason? }

// resolveAdminUser resolves the session user and confirms they're an admin.
func resolveAdminUser(r *http.Request, app *App) (domain.User, bool) {
	userID, ok := resolveSessionUser(r, app)
	if !ok {
		return domain.User{}, false
	}
	u, err := app.Users.GetUser(r.Context(), userID)
	if err != nil || u.Role != domain.UserRoleAdmin {
		return domain.User{}, false
	}
	return u, true
}

// resolveModeratorUser admits admins AND moderators — the gate for delegated
// moderation tooling (the report queue, hide-activity).
func resolveModeratorUser(r *http.Request, app *App) (domain.User, bool) {
	userID, ok := resolveSessionUser(r, app)
	if !ok {
		return domain.User{}, false
	}
	u, err := app.Users.GetUser(r.Context(), userID)
	if err != nil || !u.Role.CanModerate() {
		return domain.User{}, false
	}
	return u, true
}

// presenceStaleAfter is how long after a worker's last heartbeat its presence
// record is treated as a ghost (crashed/replaced instance) and pruned. The
// worker heartbeats every ~20s; this is ~2 missed beats of tolerance, well
// under the 60s presence-KV TTL.
const presenceStaleAfter = 45 * time.Second

func mountAdminAPI(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.CreateWorkerEnrollment == nil || app.WorkerEnrollments == nil {
		logger.Info("admin api not mounted: worker enrollment not wired")
		return
	}

	// GET /api/admin/workers — connected workers (from the presence KV), with
	// the version they run and the service type (provider) they connect.
	mux.HandleFunc("GET /api/admin/workers", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		out := []map[string]any{}
		if app.NATSBus != nil {
			if kv, err := app.NATSBus.KV("cairn_worker_presence"); err == nil {
				keys, _ := kv.Keys(r.Context())
				now := time.Now()
				for _, k := range keys {
					entry, err := kv.Get(r.Context(), k)
					if err != nil {
						continue
					}
					var hb struct {
						WorkerName   string                           `json:"worker_name"`
						WorkerKey    string                           `json:"worker_key"`
						InstanceID   string                           `json:"instance_id"`
						Version      string                           `json:"version"`
						Provider     string                           `json:"provider"`
						Package      string                           `json:"package"`
						GoVersion    string                           `json:"go_version"`
						Commit       string                           `json:"commit"`
						BuildDate    string                           `json:"build_date"`
						Webhooks     bool                             `json:"webhooks"`
						Capabilities map[string]capability.Capability `json:"capabilities"`
						LastSeen     string                           `json:"last_seen"`
					}
					if json.Unmarshal(entry.Value, &hb) != nil {
						continue
					}
					// Only surface LIVE workers. A presence key whose last heartbeat
					// is older than the staleness window is a ghost (a crashed or
					// just-replaced instance — its key lingers until the 60s KV TTL).
					// Skip it AND actively prune it so the admin view cleans up old
					// workers instead of showing duplicates as "online".
					if ts, perr := time.Parse(time.RFC3339, hb.LastSeen); perr == nil {
						if now.Sub(ts) > presenceStaleAfter {
							_ = kv.Delete(r.Context(), k)
							continue
						}
					}
					out = append(out, map[string]any{
						"worker_name": hb.WorkerName,
						"worker_key":  hb.WorkerKey,
						"instance_id": hb.InstanceID,
						"version":     hb.Version,
						"provider":    hb.Provider,
						"package":     hb.Package,
						// Build info is informational (may differ across pooled
						// instances); version+name+provider are the identity.
						"go_version": hb.GoVersion,
						"commit":     hb.Commit,
						"build_date": hb.BuildDate,
						// The worker advertises whether it owns its webhook subject;
						// the UI surfaces the webhook URL only for webhook-capable
						// workers.
						"webhooks": hb.Webhooks,
						// Per-data-type capability manifest (read/write/backfill).
						// Provider-neutral; drives the "this provider gives you …"
						// display and capability-aware sync routing.
						"capabilities": hb.Capabilities,
						"last_seen":    hb.LastSeen,
					})
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"workers": out})
	})

	mux.HandleFunc("GET /api/admin/worker-enrollments", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		list, err := app.WorkerEnrollments.ListEnrollments(r.Context(), port.ListEnrollmentsFilter{
			IncludeRevoked: true,
			IncludeExpired: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(list))
		for _, e := range list {
			out = append(out, enrollmentResponseJSON(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"enrollments": out})
	})

	mux.HandleFunc("POST /api/admin/worker-enrollments", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := resolveAdminUser(r, app)
		if !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		// Provider + version are WORKER-reported (heartbeat metadata), NOT
		// admin-set. The admin supplies only a name, expiry, and note.
		var body struct {
			Name           string `json:"name"`
			ExpiresInHours int    `json:"expires_in_hours"`
			Note           string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if body.ExpiresInHours <= 0 {
			body.ExpiresInHours = 365 * 24 // default 365 days (the use-case cap)
		}
		result, err := app.CreateWorkerEnrollment.Execute(r.Context(), enrollment.CreateEnrollmentInput{
			Name:      body.Name,
			ExpiresIn: time.Duration(body.ExpiresInHours) * time.Hour,
			Note:      body.Note,
		})
		if err != nil {
			http.Error(w, "create enrollment failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Info("worker enrollment created via admin UI", "admin", admin.ID, "name", body.Name)
		writeJSON(w, http.StatusCreated, map[string]any{
			"enrollment": enrollmentResponseJSON(result.Enrollment),
			"token":      result.Token, // shown once
			"warning":    "store this token now — it will not be shown again",
		})
	})

	mux.HandleFunc("POST /api/admin/worker-enrollments/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := resolveAdminUser(r, app)
		if !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		adminID := admin.ID
		if err := app.RevokeWorkerEnrollment.Execute(r.Context(), enrollment.RevokeInput{
			EnrollmentID: domain.WorkerEnrollmentID(id),
			By:           &adminID,
			Reason:       body.Reason,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Prolong: extend an enrollment's expiry (works even after it lapsed).
	mux.HandleFunc("POST /api/admin/worker-enrollments/{id}/prolong", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body struct {
			ExpiresInHours int `json:"expires_in_hours"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ExpiresInHours <= 0 {
			body.ExpiresInHours = 365 * 24
		}
		if body.ExpiresInHours > 365*24 {
			body.ExpiresInHours = 365 * 24 // mirror the create cap
		}
		newExpiry := time.Now().UTC().Add(time.Duration(body.ExpiresInHours) * time.Hour)
		if err := app.WorkerEnrollments.ExtendEnrollment(r.Context(), domain.WorkerEnrollmentID(id), newExpiry); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expires_at": newExpiry.Format(time.RFC3339)})
	})

	logger.Info("admin api endpoints mounted (session+admin gated)")
}
