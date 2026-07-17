package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// AthleteProfileRepo implements port.AthleteProfileRepo on the athlete_metrics
// table (migration 23). Each row is one dated measurement of one metric.
type AthleteProfileRepo struct {
	pool *pgxpool.Pool
}

func NewAthleteProfileRepo(pool *pgxpool.Pool) *AthleteProfileRepo {
	return &AthleteProfileRepo{pool: pool}
}

func (r *AthleteProfileRepo) ListEntries(ctx context.Context, userID domain.UserID) ([]domain.AthleteMetricEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, metric_key, effective_date, value, created_at, updated_at
		  FROM athlete_metrics
		 WHERE user_id = $1
		 ORDER BY metric_key, effective_date`, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list athlete metrics: %w", err)
	}
	defer rows.Close()
	var out []domain.AthleteMetricEntry
	for rows.Next() {
		var (
			e   domain.AthleteMetricEntry
			id  uuid.UUID
			uid uuid.UUID
			key string
		)
		if err := rows.Scan(&id, &uid, &key, &e.EffectiveDate, &e.Value, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.ID = domain.AthleteMetricID(id)
		e.UserID = domain.UserID(uid)
		e.Key = domain.AthleteMetricKey(key)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *AthleteProfileRepo) UpsertEntry(ctx context.Context, e domain.AthleteMetricEntry) (domain.AthleteMetricID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO athlete_metrics (user_id, metric_key, effective_date, value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, metric_key, effective_date)
		DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		RETURNING id`,
		e.UserID.UUID(), string(e.Key), e.EffectiveDate, e.Value).Scan(&id)
	if err != nil {
		return domain.AthleteMetricID{}, fmt.Errorf("upsert athlete metric: %w", err)
	}
	return domain.AthleteMetricID(id), nil
}

func (r *AthleteProfileRepo) DeleteEntry(ctx context.Context, userID domain.UserID, id domain.AthleteMetricID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM athlete_metrics WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID())
	if err != nil {
		return fmt.Errorf("delete athlete metric: %w", err)
	}
	return nil
}
