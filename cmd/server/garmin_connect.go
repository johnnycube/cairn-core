package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// Garmin has no public OAuth API, so it connects via stored email/password
// (garminconnect / Garth SSO). Credentials live in user_provider_configs
// (client_id = email, client_secret = password, encrypted) — managed through
// the generic /api/connections CRUD like every provider — and reach the worker
// via the cairn.creds.garmin.get request/reply, never queued in a job.

const garminProvider = "garmin"

// mountGarminConnect wires the Garmin-specific health-import trigger. Account
// linking happens in the generic POST /api/connections (credential providers
// create their external account together with the connection).
func mountGarminConnect(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.UserProviderConfigs == nil || app.ExternalAccounts == nil {
		return
	}

	// Trigger a health-metric import (HRV/sleep/weight/steps/resting HR) for the
	// last N days. Publishes a job the Garmin worker pulls.
	mux.HandleFunc("POST /api/accounts/{id}/import-health", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if app.NATSBus == nil {
			http.Error(w, "worker bus unavailable", http.StatusServiceUnavailable)
			return
		}
		accID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad account id", http.StatusBadRequest)
			return
		}
		acct, err := app.ExternalAccounts.GetExternalAccount(r.Context(), domain.ExternalAccountID(accID))
		if err != nil || acct.UserID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}
		now := time.Now().UTC()
		job := map[string]any{
			"account_id": accID.String(),
			"user_id":    userID.String(),
			"provider":   acct.Provider,
			"since_unix": now.AddDate(0, 0, -days).Unix(),
			"until_unix": now.Unix(),
		}
		body, _ := json.Marshal(job)
		// Unique per request so re-triggering within the dedup window isn't dropped.
		msgID := fmt.Sprintf("import_metrics:%s:%s:%d", acct.Provider, accID.String(), now.Unix())
		if err := app.NATSBus.Publish(r.Context(), "cairn.jobs.import_metrics."+acct.Provider, msgID, body); err != nil {
			http.Error(w, "enqueue failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"queued": true, "days": days})
	})

}

// startGarminCredsHandler serves cairn.creds.garmin.get: account_id →
// {email, password}. The worker calls this per job (creds are never queued).
func startGarminCredsHandler(ctx context.Context, app *App, logger *slog.Logger) (port.Subscription, error) {
	if app.NATSBus == nil || app.ExternalAccounts == nil || app.UserProviderConfigs == nil {
		return nil, nil
	}
	sub, err := app.NATSBus.RespondTo(ctx, "cairn.creds.garmin.get", func(ctx context.Context, body []byte) ([]byte, error) {
		var req struct {
			AccountID string `json:"account_id"`
		}
		_ = json.Unmarshal(body, &req)
		reply := func(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

		accID, err := uuid.Parse(req.AccountID)
		if err != nil {
			return reply(map[string]any{"error": "bad_request"}), nil
		}
		acct, err := app.ExternalAccounts.GetExternalAccount(ctx, domain.ExternalAccountID(accID))
		if err != nil || acct.ConnectionID == nil {
			return reply(map[string]any{"error": "account_gone"}), nil
		}
		cfg, err := app.UserProviderConfigs.GetByID(ctx, *acct.ConnectionID)
		if err != nil {
			return reply(map[string]any{"error": "transient"}), nil
		}
		if cfg.SecretUnreadable || cfg.ClientSecret == "" {
			return reply(map[string]any{"error": "needs_reauth"}), nil
		}
		return reply(map[string]any{"email": cfg.ClientID, "password": cfg.ClientSecret}), nil
	})
	if err != nil {
		return nil, err
	}
	logger.Info("garmin creds handler active on cairn.creds.garmin.get")
	return sub, nil
}
