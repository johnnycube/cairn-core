package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// startReauthNotifier subscribes to cairn.events.external_account.needs_reauth
// and turns each event into a user-facing notification (in-app + email/webhook
// per the user's prefs). The event is published by the OAuth token handler when
// a worker reports a terminal refresh failure.
//
// Gating: returns (nil, nil) when the NATS bus isn't wired (single-process mode)
// or the use case isn't built.
func startReauthNotifier(ctx context.Context, app *App, logger *slog.Logger) (port.Subscription, error) {
	if app.NATSBus == nil || app.NotifyAccountNeedsReauth == nil {
		return nil, nil
	}
	log := logger.With("component", "reauth_notifier")
	log.Info("subscribing to cairn.events.external_account.needs_reauth")

	return app.NATSBus.Subscribe(ctx, port.ConsumerConfig{
		Stream:        "CAIRN_EVENTS",
		Durable:       "reauth-notifier",
		Subject:       "cairn.events.external_account.needs_reauth",
		DeliverPolicy: port.DeliverNew,
	}, func(ctx context.Context, msg port.Message) error {
		var ev struct {
			AccountID string `json:"account_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(msg.Body, &ev); err != nil {
			// Malformed event will never parse — Term it (don't redeliver).
			log.Warn("decode needs_reauth event failed", "error", err)
			return &port.TerminalError{Reason: "bad_payload", Cause: err}
		}
		accID, err := uuid.Parse(ev.AccountID)
		if err != nil {
			return &port.TerminalError{Reason: "bad_account_id", Cause: err}
		}
		if err := app.NotifyAccountNeedsReauth.Execute(ctx, domain.ExternalAccountID(accID), ev.Reason); err != nil {
			// DB blip → NAK so JetStream redelivers; the notification's dedup
			// key keeps redelivery idempotent (coalesces).
			log.Warn("create reauth notification failed", "account_id", accID, "error", err)
			return err
		}
		log.Info("reauth notification dispatched", "account_id", accID, "reason", ev.Reason)
		return nil
	})
}
