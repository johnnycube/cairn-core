package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/port"
)

// NotifyGearDecision tells a user that a provider reported gear Cairn has no
// record for, so they can create it, map it onto existing gear, or leave it.
//
// Coalescing: DedupKey = "gear_decision:<provider>:<account>:<external_id>"
// so a record re-sent with every activity folds into one notification.
type NotifyGearDecision struct {
	notifications port.NotificationRepo
	tx            port.TxManager
	deliver       *DeliverNotifications

	now   func() time.Time
	newID func() uuid.UUID
}

func NewNotifyGearDecision(
	notifications port.NotificationRepo,
	tx port.TxManager,
	deliver *DeliverNotifications,
	now func() time.Time,
	newID func() uuid.UUID,
) *NotifyGearDecision {
	now, newID = notificationClockDefaults(now, newID)
	return &NotifyGearDecision{
		notifications: notifications,
		tx:            tx,
		deliver:       deliver,
		now:           now,
		newID:         newID,
	}
}

// Execute creates + delivers the decision notification for one pending ref.
func (uc *NotifyGearDecision) Execute(ctx context.Context, ref domain.GearExternalRef) error {
	now := uc.now()
	acct := ref.ExternalAccountID
	n := domain.Notification{
		ID:           domain.NotificationID(uc.newID()),
		UserID:       ref.UserID,
		Type:         domain.NotificationTypeGearDecisionNeeded,
		Severity:     domain.NotificationSeverityInfo,
		TitleI18nKey: "notification.gear_decision.title",
		BodyI18nKey:  "notification.gear_decision.body",
		I18nParams: map[string]string{
			"provider":            ref.Provider,
			"gear_name":           ref.ProviderName,
			"external_id":         ref.ExternalID,
			"external_account_id": acct.String(),
		},
		ExternalAccountID: &acct,
		DedupKey:          fmt.Sprintf("gear_decision:%s:%s:%s", ref.Provider, acct, ref.ExternalID),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	batch := []domain.Notification{n}
	if err := uc.tx.InTx(ctx, func(ctx context.Context) error {
		return uc.notifications.SaveNotifications(ctx, batch)
	}); err != nil {
		return fmt.Errorf("persist gear-decision notification: %w", err)
	}
	uc.deliver.Execute(ctx, batch)
	return nil
}
