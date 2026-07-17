package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// QuotaRepo implements port.QuotaRepo over user_quotas.
type QuotaRepo struct{ pool *pgxpool.Pool }

func NewQuotaRepo(pool *pgxpool.Pool) *QuotaRepo { return &QuotaRepo{pool: pool} }

func (r *QuotaRepo) GetMaxActivities(ctx context.Context, userID domain.UserID) (*int, error) {
	db := dbtx(ctx, r.pool)
	var max *int
	err := db.QueryRow(ctx,
		`SELECT max_activities FROM user_quotas WHERE user_id = $1`, userID.UUID()).Scan(&max)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get max activities: %w", err)
	}
	return max, nil
}

func (r *QuotaRepo) SetMaxActivities(ctx context.Context, userID domain.UserID, max *int) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO user_quotas (user_id, max_activities, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (user_id) DO UPDATE SET max_activities = EXCLUDED.max_activities, updated_at = now()`,
		userID.UUID(), max)
	if err != nil {
		return fmt.Errorf("set max activities: %w", err)
	}
	return nil
}
