package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/domain/capability"
)

// Gear sync: core asks a provider worker for the account's full gear list
// (cairn.jobs.import_gear.<provider>, docs/gear-v0.4.0.md). Published on
// account connect, on the user's "Sync gear" action and alongside every
// reconcile — but only when a live worker for the provider declares the
// Gear data type, because CAIRN_JOBS is a work queue and a job nobody
// consumes would sit there forever.

var errGearNotSupported = errors.New("no connected worker for this provider imports gear")

// workerDeclares reports whether any live worker for provider can read dt,
// from the presence KV the workers heartbeat into.
func workerDeclares(ctx context.Context, app *App, provider string, dt capability.DataType) bool {
	if app.NATSBus == nil {
		return false
	}
	kv, err := app.NATSBus.KV("cairn_worker_presence")
	if err != nil {
		return false
	}
	keys, _ := kv.Keys(ctx)
	now := time.Now()
	for _, k := range keys {
		entry, err := kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var hb adminAPIHeartbeat
		if json.Unmarshal(entry.Value, &hb) != nil || hb.Provider != provider {
			continue
		}
		if ts, perr := time.Parse(time.RFC3339, hb.LastSeen); perr == nil && now.Sub(ts) > presenceStaleAfter {
			continue
		}
		if hb.Capabilities[string(dt)].Read {
			return true
		}
	}
	return false
}

// importGearJobBody is the JSON job a worker receives on import_gear.
type importGearJobBody struct {
	JobID     string    `json:"job_id"`
	AccountID string    `json:"account_id"`
	UserID    string    `json:"user_id"`
	Provider  string    `json:"provider"`
	Reason    string    `json:"reason"`
	IssuedAt  time.Time `json:"issued_at"`
}

// publishImportGear enqueues an import_gear job for the account. Returns
// errGearNotSupported when no live worker for the provider declares gear.
func publishImportGear(ctx context.Context, app *App, acct domain.ExternalAccount, reason string) error {
	if app.NATSBus == nil {
		return errors.New("worker bus unavailable")
	}
	if !workerDeclares(ctx, app, acct.Provider, capability.DataTypeGear) {
		return errGearNotSupported
	}
	now := time.Now().UTC()
	body, err := json.Marshal(importGearJobBody{
		JobID:     uuid.NewString(),
		AccountID: acct.ID.String(),
		UserID:    acct.UserID.String(),
		Provider:  acct.Provider,
		Reason:    reason,
		IssuedAt:  now,
	})
	if err != nil {
		return err
	}
	// Minute-bucketed like reconcile: a tick and a manual trigger within the
	// same minute collapse to one job.
	msgID := fmt.Sprintf("import_gear:%s:%s", acct.ID, now.Format("2006-01-02T15:04"))
	return app.NATSBus.Publish(ctx, "cairn.jobs.import_gear."+acct.Provider, msgID, body)
}

// refreshGearTotals rebuilds the user's gear totals after any path that
// re-merged an activity outside the ingest router (detach, field pins,
// source-from). Best-effort, logs on failure.
func refreshGearTotals(ctx context.Context, app *App, logger *slog.Logger, userID domain.UserID) {
	if app.Gear == nil {
		return
	}
	if err := app.Gear.RecomputeTotals(ctx, userID); err != nil {
		logger.Warn("gear totals recompute failed", "user_id", userID, "error", err)
	}
}

// gearSyncHook is the reconcile-time publisher; a provider whose worker does
// not import gear is simply skipped.
func gearSyncHook(app *App) func(ctx context.Context, a domain.ExternalAccount) error {
	return func(ctx context.Context, a domain.ExternalAccount) error {
		err := publishImportGear(ctx, app, a, "scheduled")
		if errors.Is(err, errGearNotSupported) {
			return nil
		}
		return err
	}
}

// syncGearOnConnect publishes the first gear import for a freshly linked
// account. Best-effort: the next reconcile repeats it.
func syncGearOnConnect(ctx context.Context, app *App, logger *slog.Logger, acctID domain.ExternalAccountID) {
	if app.ExternalAccounts == nil {
		return
	}
	acct, err := app.ExternalAccounts.GetExternalAccount(ctx, acctID)
	if err != nil {
		return
	}
	if err := publishImportGear(ctx, app, acct, "connect"); err != nil && !errors.Is(err, errGearNotSupported) {
		logger.Warn("gear sync on connect failed", "account_id", acctID, "error", err)
	}
}

// gearSyncForUser is the GearService.SyncGear backend: ownership check, then
// publish. queued=false with a nil error means no worker imports gear.
func gearSyncForUser(app *App) func(ctx context.Context, userID domain.UserID, accountID domain.ExternalAccountID) (bool, error) {
	return func(ctx context.Context, userID domain.UserID, accountID domain.ExternalAccountID) (bool, error) {
		if app.NATSBus == nil {
			return false, errors.New("worker bus unavailable")
		}
		acct, err := app.ExternalAccounts.GetExternalAccount(ctx, accountID)
		if err != nil || acct.UserID != userID {
			return false, domain.ErrNotFound
		}
		err = publishImportGear(ctx, app, acct, "manual")
		if errors.Is(err, errGearNotSupported) {
			return false, nil
		}
		return err == nil, err
	}
}

// mountGearSync serves POST /api/accounts/{id}/sync-gear for the connections
// page (same shape as the Garmin health import trigger).
func mountGearSync(mux *http.ServeMux, app *App) {
	if app.ExternalAccounts == nil {
		return
	}
	sync := gearSyncForUser(app)
	mux.HandleFunc("POST /api/accounts/{id}/sync-gear", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		accID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad account id", http.StatusBadRequest)
			return
		}
		queued, err := sync(r.Context(), userID, domain.ExternalAccountID(accID))
		switch {
		case errors.Is(err, domain.ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case err != nil:
			http.Error(w, "enqueue failed", http.StatusInternalServerError)
		case !queued:
			http.Error(w, errGearNotSupported.Error(), http.StatusConflict)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"queued": true})
		}
	})
}
