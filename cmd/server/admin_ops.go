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

// mountAdminOps wires the operator-only maintenance endpoints under the
// session+admin-gated /api/admin/* surface (resolveAdminUser). These have no
// other home — first-time NATS account-key setup, key rotation, dead-letter
// inspection/replay, and an on-demand geocode backfill. (The legacy token-gated
// /admin/* smoketest layer was removed; user-facing recompute/detach/listings
// now live on the Connect-RPC AdminService + the per-activity /api/* surface.)
func mountAdminOps(mux *http.ServeMux, app *App, logger *slog.Logger) {
	adminGate := func(w http.ResponseWriter, r *http.Request) bool {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return false
		}
		return true
	}

	// On-demand geocode start-location backfill (the continuous backfiller
	// already drains on a timer; this runs one batch immediately). Async.
	mux.HandleFunc("POST /api/admin/geocode/backfill", func(w http.ResponseWriter, r *http.Request) {
		if !adminGate(w, r) {
			return
		}
		if app.ComputeStartPlace == nil {
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
			n, err := app.ComputeStartPlace.BackfillBatch(ctx, limit)
			if err != nil {
				logger.Warn("admin geocode backfill failed", "error", err)
				return
			}
			logger.Info("admin geocode backfill complete", "resolved", n)
		}(limit)
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "limit": limit})
	})

	// First-time NATS account signing-key setup. Returns the account public key
	// to paste into nats-server.conf auth_callout.issuer.
	mux.HandleFunc("POST /api/admin/nats/bootstrap-account-key", func(w http.ResponseWriter, r *http.Request) {
		if !adminGate(w, r) {
			return
		}
		if app.SigningKeys == nil || app.SecretBox == nil {
			http.Error(w, "signing-key repo / secret box not wired", http.StatusServiceUnavailable)
			return
		}
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
	})

	// Rotate the NATS account signing key (mint new active + deactivate old,
	// atomic) and reload the live issuer's cache. Trust BOTH keys during overlap.
	mux.HandleFunc("POST /api/admin/nats/rotate-account-key", func(w http.ResponseWriter, r *http.Request) {
		if !adminGate(w, r) {
			return
		}
		if app.SigningKeys == nil || app.SecretBox == nil {
			http.Error(w, "signing-key repo / secret box not wired", http.StatusServiceUnavailable)
			return
		}
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
	})

	// Dead-letter inspection + replay.
	mux.HandleFunc("GET /api/admin/dlq", func(w http.ResponseWriter, r *http.Request) {
		if !adminGate(w, r) {
			return
		}
		if app.DeadLetters == nil {
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
		list, err := app.DeadLetters.List(r.Context(), postgres.DLQListInput{
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
	})

	mux.HandleFunc("POST /api/admin/dlq/{id}/replay", func(w http.ResponseWriter, r *http.Request) {
		if !adminGate(w, r) {
			return
		}
		if app.DeadLetters == nil || app.NATSBus == nil {
			http.Error(w, "dlq + nats not wired", http.StatusServiceUnavailable)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		job, err := app.DeadLetters.Get(r.Context(), domain.DeadLetteredJobID(id))
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "load failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(job.Payload) == 0 {
			http.Error(w, "captured row has no payload to replay", http.StatusUnprocessableEntity)
			return
		}
		newMsgID := fmt.Sprintf("replay:%s:%d", job.ID.String(), time.Now().Unix())
		if err := app.NATSBus.Publish(r.Context(), "cairn.replay.unsorted", newMsgID, job.Payload); err != nil {
			http.Error(w, "publish failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := app.DeadLetters.MarkReplayed(r.Context(), job.ID, nil, time.Now().UTC()); err != nil {
			logger.Warn("replay published but mark failed", "id", job.ID, "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": job.ID.String(), "replayed": true, "new_msg_id": newMsgID})
	})

	logger.Info("admin ops endpoints mounted (/api/admin/{geocode,nats,dlq})")
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
