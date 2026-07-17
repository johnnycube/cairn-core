package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// startAccountLookupHandler wires the NATS responder that resolves a provider's
// owner/athlete id into an internal account + user id. Workers call this from
// their webhook handlers (a webhook carries only the provider-side owner_id) so
// they can publish a fetch job for the right account. Without it, webhook
// imports time out and fall back to reconcile-polling (hours of latency).
//
//	subject:  cairn.accounts.lookup_by_provider_ext
//	request:  {"provider": "strava", "external_id": "12345"}
//	reply:    {"account_id": "uuid", "user_id": "uuid"} | {"error": "..."}
func startAccountLookupHandler(
	ctx context.Context,
	app *App,
	logger *slog.Logger,
) (port.Subscription, error) {
	if app.NATSBus == nil || app.ExternalAccounts == nil {
		return nil, nil
	}
	log := logger.With("component", "account_lookup_handler")

	sub, err := app.NATSBus.RespondTo(ctx, "cairn.accounts.lookup_by_provider_ext",
		func(ctx context.Context, body []byte) ([]byte, error) {
			var req struct {
				Provider       string `json:"provider"`
				ExternalID     string `json:"external_id"`
				SubscriptionID string `json:"subscription_id"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return accountLookupErr("bad_request"), nil
			}
			if req.Provider == "" || (req.ExternalID == "" && req.SubscriptionID == "") {
				return accountLookupErr("provider and external_id or subscription_id required"), nil
			}
			// Prefer the unambiguous subscription_id path (#50): the same
			// provider athlete may be linked by multiple connections, so
			// owner_id alone can resolve to the wrong one. Fall back to
			// owner_id only when the subscription is unknown.
			var (
				acct domain.ExternalAccount
				err  error
			)
			if req.SubscriptionID != "" {
				acct, err = app.ExternalAccounts.FindBySubscription(ctx, req.Provider, req.SubscriptionID)
				if errors.Is(err, domain.ErrNotFound) && req.ExternalID != "" {
					log.Warn("subscription unknown, falling back to external_id",
						"provider", req.Provider, "subscription_id", req.SubscriptionID)
					acct, err = app.ExternalAccounts.FindByProviderAndExternalID(ctx, req.Provider, req.ExternalID)
				}
			} else {
				acct, err = app.ExternalAccounts.FindByProviderAndExternalID(ctx, req.Provider, req.ExternalID)
			}
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return accountLookupErr("not_found"), nil
				}
				log.Warn("account lookup failed", "provider", req.Provider, "error", err)
				return accountLookupErr("lookup_failed"), nil
			}
			// Auto-import suspended for this account (#93): refuse the lookup so
			// the worker drops the webhook-driven fetch. The account stays
			// linked; manual sync still works (it doesn't go through here).
			if !acct.AutoImportEnabled {
				return accountLookupErr("auto_import_suspended"), nil
			}
			// Self-heal the subscription → account mapping (#50): the first
			// event that carried a subscription_id but resolved via the
			// owner_id fallback records the binding, so every subsequent event
			// for this connection resolves unambiguously via FindBySubscription.
			if req.SubscriptionID != "" && acct.WebhookSubscriptionID != req.SubscriptionID {
				if regErr := app.ExternalAccounts.RegisterWebhookSubscription(
					ctx, req.Provider, req.SubscriptionID, acct.ID); regErr != nil {
					log.Warn("self-heal subscription mapping failed",
						"provider", req.Provider, "account_id", acct.ID.String(), "error", regErr)
				} else {
					log.Info("recorded webhook subscription mapping",
						"provider", req.Provider, "account_id", acct.ID.String(),
						"subscription_id", req.SubscriptionID)
				}
			}
			out, _ := json.Marshal(map[string]string{
				"account_id": acct.ID.String(),
				"user_id":    acct.UserID.String(),
			})
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	log.Info("account lookup handler active on cairn.accounts.lookup_by_provider_ext")
	return sub, nil
}

func accountLookupErr(reason string) []byte {
	b, _ := json.Marshal(map[string]string{"error": reason})
	return b
}
