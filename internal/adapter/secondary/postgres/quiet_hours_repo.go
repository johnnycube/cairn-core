package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// QuietHoursRepo implements port.QuietHoursRepo over notification_quiet_hours
// (migrations 10 + 30).
type QuietHoursRepo struct {
	pool *pgxpool.Pool
}

func NewQuietHoursRepo(pool *pgxpool.Pool) *QuietHoursRepo {
	return &QuietHoursRepo{pool: pool}
}

func (r *QuietHoursRepo) Get(ctx context.Context, userID domain.UserID) (domain.QuietHours, error) {
	db := dbtx(ctx, r.pool)
	var (
		q    domain.QuietHours
		days []int32
	)
	err := db.QueryRow(ctx,
		`SELECT enabled, start_minute, end_minute, days_of_week, tz
		   FROM notification_quiet_hours WHERE user_id = $1`,
		userID.UUID(),
	).Scan(&q.Enabled, &q.StartMinute, &q.EndMinute, &days, &q.TZ)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.QuietHours{UserID: userID, TZ: "UTC"}, nil
	}
	if err != nil {
		return domain.QuietHours{}, fmt.Errorf("get quiet hours: %w", err)
	}
	q.UserID = userID
	q.DaysOfWeek = make([]int, 0, len(days))
	for _, d := range days {
		q.DaysOfWeek = append(q.DaysOfWeek, int(d))
	}
	return q, nil
}

func (r *QuietHoursRepo) Upsert(ctx context.Context, q domain.QuietHours) error {
	db := dbtx(ctx, r.pool)
	days := make([]int32, 0, len(q.DaysOfWeek))
	for _, d := range q.DaysOfWeek {
		days = append(days, int32(d))
	}
	tz := q.TZ
	if tz == "" {
		tz = "UTC"
	}
	_, err := db.Exec(ctx,
		`INSERT INTO notification_quiet_hours (user_id, enabled, start_minute, end_minute, days_of_week, tz, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now())
		 ON CONFLICT (user_id) DO UPDATE SET
		     enabled = EXCLUDED.enabled,
		     start_minute = EXCLUDED.start_minute,
		     end_minute = EXCLUDED.end_minute,
		     days_of_week = EXCLUDED.days_of_week,
		     tz = EXCLUDED.tz,
		     updated_at = now()`,
		q.UserID.UUID(), q.Enabled, q.StartMinute, q.EndMinute, days, tz,
	)
	if err != nil {
		return fmt.Errorf("upsert quiet hours: %w", err)
	}
	return nil
}
