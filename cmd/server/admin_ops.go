package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	natsadapter "github.com/johnnycube/cairn-core/internal/adapter/secondary/nats"
	"github.com/johnnycube/cairn-core/internal/adapter/secondary/postgres"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// adminOpsHandlers holds the dependencies of the admin maintenance routes.
type adminOpsHandlers struct {
	app    *App
	logger *slog.Logger
}

// mountAdminOps wires the operator-only maintenance endpoints under the
// session+admin-gated /api/admin/* surface (resolveAdminUser). These have no
// other home — first-time NATS account-key setup, key rotation, dead-letter
// inspection/replay, and an on-demand geocode backfill. (The legacy token-gated
// /admin/* smoketest layer was removed; user-facing recompute/detach/listings
// now live on the Connect-RPC AdminService + the per-activity /api/* surface.)
func mountAdminOps(mux *http.ServeMux, app *App, logger *slog.Logger) {
	h := &adminOpsHandlers{app: app, logger: logger}

	// On-demand geocode start-location backfill (the continuous backfiller
	// already drains on a timer; this runs one batch immediately). Async.
	mux.HandleFunc("POST /api/admin/geocode/backfill", h.geocodeBackfill)

	// First-time NATS account signing-key setup. Returns the account public key
	// to paste into nats-server.conf auth_callout.issuer.
	mux.HandleFunc("POST /api/admin/nats/bootstrap-account-key", h.bootstrapAccountKey)

	// Rotate the NATS account signing key (mint new active + deactivate old,
	// atomic) and reload the live issuer's cache. Trust BOTH keys during overlap.
	mux.HandleFunc("POST /api/admin/nats/rotate-account-key", h.rotateAccountKey)

	// Dead-letter inspection + replay.
	mux.HandleFunc("GET /api/admin/dlq", h.listDLQ)
	mux.HandleFunc("POST /api/admin/dlq/{id}/replay", h.replayDLQ)

	logger.Info("admin ops endpoints mounted (/api/admin/{geocode,nats,dlq})")
}

// gate writes 403 and returns false unless the caller is an admin.
func (h *adminOpsHandlers) gate(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := resolveAdminUser(r, h.app); !ok {
		http.Error(w, "admin only", http.StatusForbidden)
		return false
	}
	return true
}

func (h *adminOpsHandlers) geocodeBackfill(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.app.ComputeStartPlace == nil {
		http.Error(w, "geocoder disabled", http.StatusServiceUnavailable)
		return
	}
	limit := parseIntQuery(r, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	go func(limit int) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		n, err := h.app.ComputeStartPlace.BackfillBatch(ctx, limit)
		if err != nil {
			h.logger.Warn("admin geocode backfill failed", "error", err)
			return
		}
		h.logger.Info("admin geocode backfill complete", "resolved", n)
	}(limit)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "limit": limit})
}

// signingReady writes 503 and returns false when the signing-key deps are missing.
func (h *adminOpsHandlers) signingReady(w http.ResponseWriter, r *http.Request) bool {
	if !h.gate(w, r) {
		return false
	}
	if h.app.SigningKeys == nil || h.app.SecretBox == nil {
		http.Error(w, "signing-key repo / secret box not wired", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (h *adminOpsHandlers) bootstrapAccountKey(w http.ResponseWriter, r *http.Request) {
	if !h.signingReady(w, r) {
		return
	}
	app := h.app
	if existing, err := app.SigningKeys.GetActive(r.Context(), domain.SigningKeyPurposeNATSAccount); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"public_key": existing.PublicKey,
			"created_at": existing.CreatedAt.UTC(),
			"action":     "no_change",
			"note":       "active signing key already exists",
		})
		return
	}
	created, err := natsadapter.BootstrapAccountKey(r.Context(), app.SigningKeys, app.SecretBox, adminUserPtr(r, app))
	if err != nil {
		http.Error(w, "bootstrap failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"public_key": created.PublicKey,
		"created_at": created.CreatedAt.UTC(),
		"action":     "created",
		"note":       "copy this public key into nats-server.conf auth_callout.issuer",
	})
}

func (h *adminOpsHandlers) rotateAccountKey(w http.ResponseWriter, r *http.Request) {
	if !h.signingReady(w, r) {
		return
	}
	app := h.app
	var newKey domain.SigningKey
	var oldPub string
	err := app.Tx.InTx(r.Context(), func(ctx context.Context) error {
		var e error
		newKey, oldPub, e = natsadapter.RotateAccountKey(ctx, app.SigningKeys, app.SecretBox, adminUserPtr(r, app))
		return e
	})
	if err != nil {
		http.Error(w, "rotate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if app.NATSCredentialIssuer != nil {
		app.NATSCredentialIssuer.InvalidateCache()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"new_public_key": newKey.PublicKey,
		"old_public_key": oldPub,
		"action":         "rotated",
		"note":           "trust BOTH keys in nats-server.conf during the overlap, then drop the old one once all workers have renewed",
	})
}

func (h *adminOpsHandlers) listDLQ(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.app.DeadLetters == nil {
		http.Error(w, "dlq repo not wired", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	var beforeTS time.Time
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			beforeTS = t
		}
	}
	list, err := h.app.DeadLetters.List(r.Context(), postgres.DLQListInput{
		Stream:            q.Get("stream"),
		Subject:           q.Get("subject"),
		IncludeReplayed:   q.Get("include_replayed") == "true",
		Limit:             parseIntQuery(r, "limit", 50),
		BeforeFirstSeenAt: beforeTS,
	})
	if err != nil {
		http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, j := range list {
		out = append(out, dlqJobToJSON(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

// loadDLQJob parses {id} and loads the dead-lettered job, writing errors itself.
func (h *adminOpsHandlers) loadDLQJob(w http.ResponseWriter, r *http.Request) (domain.DeadLetteredJob, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return domain.DeadLetteredJob{}, false
	}
	job, err := h.app.DeadLetters.Get(r.Context(), domain.DeadLetteredJobID(id))
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return domain.DeadLetteredJob{}, false
	}
	if err != nil {
		http.Error(w, "load failed: "+err.Error(), http.StatusInternalServerError)
		return domain.DeadLetteredJob{}, false
	}
	return job, true
}

func (h *adminOpsHandlers) replayDLQ(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.app.DeadLetters == nil || h.app.NATSBus == nil {
		http.Error(w, "dlq + nats not wired", http.StatusServiceUnavailable)
		return
	}
	job, ok := h.loadDLQJob(w, r)
	if !ok {
		return
	}
	if len(job.Payload) == 0 {
		http.Error(w, "captured row has no payload to replay", http.StatusUnprocessableEntity)
		return
	}
	newMsgID := fmt.Sprintf("replay:%s:%d", job.ID.String(), time.Now().Unix())
	if err := h.app.NATSBus.Publish(r.Context(), "cairn.replay.unsorted", newMsgID, job.Payload); err != nil {
		http.Error(w, "publish failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.app.DeadLetters.MarkReplayed(r.Context(), job.ID, nil, time.Now().UTC()); err != nil {
		h.logger.Warn("replay published but mark failed", "id", job.ID, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": job.ID.String(), "replayed": true, "new_msg_id": newMsgID})
}

// adminUserPtr returns the resolved admin user's id pointer (for audit trails),
// or nil when it can't be resolved (the caller already passed adminGate).
func adminUserPtr(r *http.Request, app *App) *domain.UserID {
	if u, ok := resolveAdminUser(r, app); ok {
		id := u.ID
		return &id
	}
	return nil
}

func dlqJobToJSON(j domain.DeadLetteredJob) map[string]any {
	out := map[string]any{
		"id":              j.ID.String(),
		"stream":          j.Stream,
		"subject":         j.Subject,
		"consumer":        j.Consumer,
		"msg_id":          j.MsgID,
		"delivered_count": j.DeliveredCount,
		"last_error":      j.LastError,
		"first_seen_at":   j.FirstSeenAt.Format(time.RFC3339),
		"last_seen_at":    j.LastSeenAt.Format(time.RFC3339),
		"replay_count":    j.ReplayCount,
	}
	if j.ReplayedAt != nil {
		out["replayed_at"] = j.ReplayedAt.Format(time.RFC3339)
	}
	return out
}

// enrollmentResponseJSON shapes a WorkerEnrollment for the /api/admin/worker-
// enrollments responses.
func enrollmentResponseJSON(e domain.WorkerEnrollment) map[string]any {
	out := map[string]any{
		"id":                  e.ID.String(),
		"provider":            e.Provider,
		"name":                e.Name,
		"version":             e.Version,
		"worker_key":          e.WorkerKey(),
		"worker_name_pattern": e.WorkerNamePattern,
		"permission_template": e.PermissionTemplate,
		"created_at":          e.CreatedAt.Format(time.RFC3339),
		"note":                e.Note,
		"expires_at":          e.ExpiresAt.Format(time.RFC3339),
		"max_uses":            e.MaxUses,
		"uses":                e.Uses,
		"token_hash_hex":      fmt.Sprintf("%x", e.TokenHash),
		"is_revoked":          e.IsRevoked(),
		"reenroll_required":   e.NeedsReenroll(),
	}
	if e.CreatedByUserID != nil {
		out["created_by_user_id"] = e.CreatedByUserID.String()
	}
	if e.RevokedAt != nil {
		out["revoked_at"] = e.RevokedAt.Format(time.RFC3339)
		out["revoked_reason"] = e.RevokedReason
	}
	if e.RevokedByUserID != nil {
		out["revoked_by_user_id"] = e.RevokedByUserID.String()
	}
	return out
}
