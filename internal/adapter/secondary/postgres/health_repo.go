package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/domain/capability"
	"github.com/johnnycube/cairn-core/internal/domain/health"
)

// HealthRepo implements port.HealthRepo over the health_samples hypertable.
type HealthRepo struct{ pool *pgxpool.Pool }

func NewHealthRepo(pool *pgxpool.Pool) *HealthRepo { return &HealthRepo{pool: pool} }

func (r *HealthRepo) SaveSamples(ctx context.Context, userID domain.UserID, accountID *domain.ExternalAccountID, samples []health.Sample) error {
	if len(samples) == 0 {
		return nil
	}
	db := dbtx(ctx, r.pool)
	var acct any
	if accountID != nil {
		acct = accountID.UUID()
	}
	for _, s := range samples {
		_, err := db.Exec(ctx, `
			INSERT INTO health_samples (user_id, data_type, ts, provider, external_account_id, value, unit)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (user_id, data_type, ts, provider, external_id)
			DO UPDATE SET value = EXCLUDED.value, unit = EXCLUDED.unit`,
			userID.UUID(), string(s.DataType), s.Timestamp.UTC(), s.Provider, acct, s.Value, s.Unit,
		)
		if err != nil {
			return fmt.Errorf("save health sample: %w", err)
		}
	}
	return nil
}

func (r *HealthRepo) ListSamples(ctx context.Context, userID domain.UserID, dataType capability.DataType, from, to time.Time) ([]health.Sample, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx, `
		SELECT data_type, ts, provider, value, unit
		  FROM health_samples
		 WHERE user_id = $1 AND data_type = $2 AND ts >= $3 AND ts < $4
		 ORDER BY ts`,
		userID.UUID(), string(dataType), from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("list health samples: %w", err)
	}
	defer rows.Close()
	var out []health.Sample
	for rows.Next() {
		var (
			dt, provider, unit string
			ts                 time.Time
			value              *float64
		)
		if err := rows.Scan(&dt, &ts, &provider, &value, &unit); err != nil {
			return nil, fmt.Errorf("scan health sample: %w", err)
		}
		out = append(out, health.Sample{
			UserID:    userID,
			DataType:  capability.DataType(dt),
			Timestamp: ts,
			Provider:  provider,
			Value:     value,
			Unit:      unit,
		})
	}
	return out, rows.Err()
}
