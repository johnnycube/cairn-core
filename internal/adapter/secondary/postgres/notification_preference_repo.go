package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// NotificationPreferenceRepo implements port.NotificationPreferenceRepo over
// the notification_preferences table (migration 10). The matrix is sparse —
// only rows the user has explicitly set exist; everything else resolves to
// domain.DefaultChannelEnabled.
type NotificationPreferenceRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationPreferenceRepo(pool *pgxpool.Pool) *NotificationPreferenceRepo {
	return &NotificationPreferenceRepo{pool: pool}
}

func (r *NotificationPreferenceRepo) ListForUser(
	ctx context.Context, userID domain.UserID,
) ([]domain.NotificationPreference, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT event_type, channel, enabled, min_severity
		   FROM notification_preferences
		  WHERE user_id = $1`,
		userID.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	defer rows.Close()

	var out []domain.NotificationPreference
	for rows.Next() {
		var (
			eventType int
			channel   string
			enabled   bool
			minSev    string
		)
		if err := rows.Scan(&eventType, &channel, &enabled, &minSev); err != nil {
			return nil, fmt.Errorf("scan notification preference: %w", err)
		}
		out = append(out, domain.NotificationPreference{
			UserID:      userID,
			Type:        domain.NotificationType(eventType),
			Channel:     domain.NotificationChannel(channel),
			Enabled:     enabled,
			MinSeverity: domain.NotificationSeverity(minSev),
		})
	}
	return out, rows.Err()
}

func (r *NotificationPreferenceRepo) Upsert(ctx context.Context, p domain.NotificationPreference) error {
	minSev := p.MinSeverity
	if !minSev.Valid() {
		minSev = domain.NotificationSeverityInfo
	}
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO notification_preferences (user_id, event_type, channel, enabled, min_severity)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, event_type, channel)
		 DO UPDATE SET enabled = EXCLUDED.enabled, min_severity = EXCLUDED.min_severity`,
		p.UserID.UUID(), int(p.Type), string(p.Channel), p.Enabled, string(minSev),
	)
	if err != nil {
		return fmt.Errorf("upsert notification preference: %w", err)
	}
	return nil
}
