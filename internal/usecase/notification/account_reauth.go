package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// NotifyAccountNeedsReauth creates (and side-channel delivers) a notification
// when an external account's OAuth token can no longer be refreshed — i.e. the
// connection needs the user to re-authorise. It's driven by the
// `cairn.events.external_account.needs_reauth` event a worker publishes after a
// terminal refresh failure (invalid_grant etc.).
//
// Without this, a broken connection fails silently: imports just stop. This is
// the creator side for NotificationTypeExternalAccountRefreshFailed (which
// defaults email-ON, since it's actionable).
//
// Coalescing: DedupKey = "account_reauth:<accountID>" with no date component,
// so repeated failures within the repo's 24h coalescing window fold into one
// notification (coalesce_count++) rather than spamming; after 24h a fresh
// reminder lands until the user fixes it.
type NotifyAccountNeedsReauth struct {
	accounts      port.ExternalAccountRepo
	notifications port.NotificationRepo
	tx            port.TxManager
	deliver       *DeliverNotifications

	now   func() time.Time
	newID func() uuid.UUID
}

func NewNotifyAccountNeedsReauth(
	accounts port.ExternalAccountRepo,
	notifications port.NotificationRepo,
	tx port.TxManager,
	deliver *DeliverNotifications,
	now func() time.Time,
	newID func() uuid.UUID,
) *NotifyAccountNeedsReauth {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = func() uuid.UUID {
			id, err := uuid.NewV7()
			if err != nil {
				return uuid.New()
			}
			return id
		}
	}
	return &NotifyAccountNeedsReauth{
		accounts:      accounts,
		notifications: notifications,
		tx:            tx,
		deliver:       deliver,
		now:           now,
		newID:         newID,
	}
}

// Execute resolves the account's owner and writes one reauth notification.
func (uc *NotifyAccountNeedsReauth) Execute(ctx context.Context, accountID domain.ExternalAccountID, reason string) error {
	acct, err := uc.accounts.GetExternalAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account %s: %w", accountID, err)
	}

	now := uc.now()
	label := acct.DisplayLabel
	if label == "" {
		label = acct.Provider
	}
	accID := acct.ID
	n := domain.Notification{
		ID:           domain.NotificationID(uc.newID()),
		UserID:       acct.UserID,
		Type:         domain.NotificationTypeExternalAccountRefreshFailed,
		Severity:     domain.NotificationSeverityError,
		TitleI18nKey: "notification.account_reauth.title",
		BodyI18nKey:  "notification.account_reauth.body",
		I18nParams: map[string]string{
			"provider": acct.Provider,
			"label":    label,
			"reason":   reason,
		},
		ExternalAccountID: &accID,
		DedupKey:          "account_reauth:" + accID.String(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	// One shared slice: SaveNotifications writes the PERSISTED id back onto the
	// element (a coalesce hit reassigns it), and the same slice flows into
	// Execute so the delivery audit references the real event_id.
	batch := []domain.Notification{n}
	if err := uc.tx.InTx(ctx, func(ctx context.Context) error {
		return uc.notifications.SaveNotifications(ctx, batch)
	}); err != nil {
		return fmt.Errorf("persist reauth notification: %w", err)
	}

	// Side-channel fan-out (email/webhook), best-effort outside the tx.
	uc.deliver.Execute(ctx, batch)
	return nil
}
