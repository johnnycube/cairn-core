package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// VisibilityRepo implements port.VisibilityRepo over user_visibility_defaults,
// activities.visibility_override, and privacy_zones (migration 31).
type VisibilityRepo struct {
	pool *pgxpool.Pool
}

func NewVisibilityRepo(pool *pgxpool.Pool) *VisibilityRepo {
	return &VisibilityRepo{pool: pool}
}

func (r *VisibilityRepo) GetUserPolicy(ctx context.Context, userID domain.UserID) (domain.VisibilityPolicy, error) {
	db := dbtx(ctx, r.pool)
	var raw []byte
	err := db.QueryRow(ctx,
		`SELECT policy FROM user_visibility_defaults WHERE user_id = $1`, userID.UUID(),
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.VisibilityPolicy{}, nil
	}
	if err != nil {
		return domain.VisibilityPolicy{}, fmt.Errorf("get user policy: %w", err)
	}
	return decodePolicy(raw)
}

func (r *VisibilityRepo) SetUserPolicy(ctx context.Context, userID domain.UserID, p domain.VisibilityPolicy) error {
	db := dbtx(ctx, r.pool)
	raw, err := json.Marshal(domain.MarshalPolicy(p))
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO user_visibility_defaults (user_id, policy, updated_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (user_id) DO UPDATE SET policy = EXCLUDED.policy, updated_at = now()`,
		userID.UUID(), raw,
	)
	if err != nil {
		return fmt.Errorf("set user policy: %w", err)
	}
	return nil
}

func (r *VisibilityRepo) GetActivityOverride(ctx context.Context, activityID domain.ActivityID) (*domain.VisibilityPolicy, error) {
	db := dbtx(ctx, r.pool)
	var raw []byte
	err := db.QueryRow(ctx,
		`SELECT visibility_override FROM activities WHERE id = $1`, activityID.UUID(),
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || len(raw) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get activity override: %w", err)
	}
	p, err := decodePolicy(raw)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *VisibilityRepo) SetActivityOverride(ctx context.Context, activityID domain.ActivityID, p *domain.VisibilityPolicy) error {
	db := dbtx(ctx, r.pool)
	var raw any
	if p != nil {
		b, err := json.Marshal(domain.MarshalPolicy(*p))
		if err != nil {
			return fmt.Errorf("marshal override: %w", err)
		}
		raw = b
	}
	_, err := db.Exec(ctx, `UPDATE activities SET visibility_override = $2 WHERE id = $1`, activityID.UUID(), raw)
	if err != nil {
		return fmt.Errorf("set activity override: %w", err)
	}
	return nil
}

func (r *VisibilityRepo) ListPrivacyZones(ctx context.Context, userID domain.UserID) ([]domain.PrivacyZone, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT id, user_id, label, lat, lng, radius_m, created_at
		   FROM privacy_zones WHERE user_id = $1 ORDER BY created_at ASC`, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list privacy zones: %w", err)
	}
	defer rows.Close()
	var out []domain.PrivacyZone
	for rows.Next() {
		var z domain.PrivacyZone
		var id, uid uuid.UUID
		if err := rows.Scan(&id, &uid, &z.Label, &z.Lat, &z.Lng, &z.RadiusM, &z.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan zone: %w", err)
		}
		z.ID = domain.PrivacyZoneID(id)
		z.UserID = domain.UserID(uid)
		out = append(out, z)
	}
	return out, rows.Err()
}

func (r *VisibilityRepo) AddPrivacyZone(ctx context.Context, z domain.PrivacyZone) (domain.PrivacyZoneID, error) {
	db := dbtx(ctx, r.pool)
	id, err := uuid.NewV7()
	if err != nil {
		return domain.PrivacyZoneID{}, err
	}
	radius := z.RadiusM
	if radius <= 0 {
		radius = 200
	}
	_, err = db.Exec(ctx,
		`INSERT INTO privacy_zones (id, user_id, label, lat, lng, radius_m) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, z.UserID.UUID(), z.Label, z.Lat, z.Lng, radius,
	)
	if err != nil {
		return domain.PrivacyZoneID{}, fmt.Errorf("add privacy zone: %w", err)
	}
	return domain.PrivacyZoneID(id), nil
}

func (r *VisibilityRepo) DeletePrivacyZone(ctx context.Context, id domain.PrivacyZoneID, userID domain.UserID) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx, `DELETE FROM privacy_zones WHERE id = $1 AND user_id = $2`, id.UUID(), userID.UUID())
	if err != nil {
		return fmt.Errorf("delete privacy zone: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func decodePolicy(raw []byte) (domain.VisibilityPolicy, error) {
	if len(raw) == 0 {
		return domain.VisibilityPolicy{}, nil
	}
	var m map[string][]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return domain.VisibilityPolicy{}, fmt.Errorf("decode policy: %w", err)
	}
	return domain.UnmarshalPolicy(m), nil
}
