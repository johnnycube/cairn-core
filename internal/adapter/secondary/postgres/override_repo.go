package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FieldOverrideRepo implements port.FieldOverrideRepo over field_source_overrides.
type FieldOverrideRepo struct{ pool *pgxpool.Pool }

func NewFieldOverrideRepo(pool *pgxpool.Pool) *FieldOverrideRepo {
	return &FieldOverrideRepo{pool: pool}
}

func (r *FieldOverrideRepo) ListForActivity(ctx context.Context, activityID domain.ActivityID) ([]domain.FieldSourceOverride, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT activity_id, field_key, source_id
		   FROM field_source_overrides WHERE activity_id = $1 ORDER BY field_key`,
		activityID.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list field overrides: %w", err)
	}
	defer rows.Close()
	var out []domain.FieldSourceOverride
	for rows.Next() {
		var aid, sid uuid.UUID
		var key string
		if err := rows.Scan(&aid, &key, &sid); err != nil {
			return nil, fmt.Errorf("scan field override: %w", err)
		}
		out = append(out, domain.FieldSourceOverride{
			ActivityID: domain.ActivityID(aid),
			FieldKey:   domain.FieldGroup(key),
			SourceID:   domain.SourceID(sid),
		})
	}
	return out, rows.Err()
}

func (r *FieldOverrideRepo) Set(ctx context.Context, o domain.FieldSourceOverride) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO field_source_overrides (activity_id, field_key, source_id, decided_by)
		 VALUES ($1, $2, $3, 'manual')
		 ON CONFLICT (activity_id, field_key)
		 DO UPDATE SET source_id = EXCLUDED.source_id, created_at = now()`,
		o.ActivityID.UUID(), string(o.FieldKey), o.SourceID.UUID(),
	)
	if err != nil {
		return fmt.Errorf("set field override: %w", err)
	}
	return nil
}

func (r *FieldOverrideRepo) Delete(ctx context.Context, activityID domain.ActivityID, fieldKey domain.FieldGroup) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM field_source_overrides WHERE activity_id = $1 AND field_key = $2`,
		activityID.UUID(), string(fieldKey),
	)
	if err != nil {
		return fmt.Errorf("delete field override: %w", err)
	}
	return nil
}

// ClassificationOverrideRepo implements port.ClassificationOverrideRepo over
// activity_classification_overrides.
type ClassificationOverrideRepo struct{ pool *pgxpool.Pool }

func NewClassificationOverrideRepo(pool *pgxpool.Pool) *ClassificationOverrideRepo {
	return &ClassificationOverrideRepo{pool: pool}
}

func (r *ClassificationOverrideRepo) Get(ctx context.Context, activityID domain.ActivityID) (domain.ClassificationOverride, error) {
	db := dbtx(ctx, r.pool)
	var (
		typ, disc, sub          sql.NullString
		virt, ebike, comm, race sql.NullBool
		dist, elev              sql.NullFloat64
		movingS                 sql.NullInt64
	)
	err := db.QueryRow(ctx,
		`SELECT type, discipline, is_virtual, is_ebike, is_commute, is_race, custom_subtype,
		        distance_m, elevation_gain_m, moving_duration_s
		   FROM activity_classification_overrides WHERE activity_id = $1`,
		activityID.UUID(),
	).Scan(&typ, &disc, &virt, &ebike, &comm, &race, &sub, &dist, &elev, &movingS)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ClassificationOverride{ActivityID: activityID}, nil
	}
	if err != nil {
		return domain.ClassificationOverride{}, fmt.Errorf("get classification override: %w", err)
	}
	o := domain.ClassificationOverride{ActivityID: activityID}
	if typ.Valid {
		t := domain.ActivityType(typ.String)
		o.Type = &t
	}
	if disc.Valid {
		d := domain.Discipline(disc.String)
		o.Discipline = &d
	}
	if virt.Valid {
		o.IsVirtual = &virt.Bool
	}
	if ebike.Valid {
		o.IsEbike = &ebike.Bool
	}
	if comm.Valid {
		o.IsCommute = &comm.Bool
	}
	if race.Valid {
		o.IsRace = &race.Bool
	}
	if sub.Valid {
		o.CustomSubtype = &sub.String
	}
	if dist.Valid {
		o.DistanceM = &dist.Float64
	}
	if elev.Valid {
		o.ElevationGainM = &elev.Float64
	}
	if movingS.Valid {
		d := time.Duration(movingS.Int64) * time.Second
		o.MovingDuration = &d
	}
	return o, nil
}

func (r *ClassificationOverrideRepo) Set(ctx context.Context, o domain.ClassificationOverride) error {
	db := dbtx(ctx, r.pool)
	if o.Empty() {
		_, err := db.Exec(ctx,
			`DELETE FROM activity_classification_overrides WHERE activity_id = $1`,
			o.ActivityID.UUID(),
		)
		if err != nil {
			return fmt.Errorf("clear classification override: %w", err)
		}
		return nil
	}
	var typ, disc, sub any
	if o.Type != nil {
		typ = string(*o.Type)
	}
	if o.Discipline != nil {
		disc = string(*o.Discipline)
	}
	if o.CustomSubtype != nil {
		sub = *o.CustomSubtype
	}
	var virt, ebike, comm, race any
	if o.IsVirtual != nil {
		virt = *o.IsVirtual
	}
	if o.IsEbike != nil {
		ebike = *o.IsEbike
	}
	if o.IsCommute != nil {
		comm = *o.IsCommute
	}
	if o.IsRace != nil {
		race = *o.IsRace
	}
	var dist, elev, movingS any
	if o.DistanceM != nil {
		dist = *o.DistanceM
	}
	if o.ElevationGainM != nil {
		elev = *o.ElevationGainM
	}
	if o.MovingDuration != nil {
		movingS = int64(o.MovingDuration.Seconds())
	}
	_, err := db.Exec(ctx,
		`INSERT INTO activity_classification_overrides
		   (activity_id, type, discipline, is_virtual, is_ebike, is_commute, is_race, custom_subtype,
		    distance_m, elevation_gain_m, moving_duration_s, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		 ON CONFLICT (activity_id) DO UPDATE SET
		   type = EXCLUDED.type, discipline = EXCLUDED.discipline,
		   is_virtual = EXCLUDED.is_virtual, is_ebike = EXCLUDED.is_ebike,
		   is_commute = EXCLUDED.is_commute, is_race = EXCLUDED.is_race,
		   custom_subtype = EXCLUDED.custom_subtype,
		   distance_m = EXCLUDED.distance_m, elevation_gain_m = EXCLUDED.elevation_gain_m,
		   moving_duration_s = EXCLUDED.moving_duration_s, updated_at = now()`,
		o.ActivityID.UUID(), typ, disc, virt, ebike, comm, race, sub, dist, elev, movingS,
	)
	if err != nil {
		return fmt.Errorf("set classification override: %w", err)
	}
	return nil
}

// SourceDenylistRepo implements port.SourceDenylistRepo over source_match_denylist.
type SourceDenylistRepo struct{ pool *pgxpool.Pool }

func NewSourceDenylistRepo(pool *pgxpool.Pool) *SourceDenylistRepo {
	return &SourceDenylistRepo{pool: pool}
}

func (r *SourceDenylistRepo) Add(ctx context.Context, e domain.SourceDenylistEntry) error {
	db := dbtx(ctx, r.pool)
	var acct any
	if e.ExternalAccountID != nil {
		acct = e.ExternalAccountID.UUID()
	}
	_, err := db.Exec(ctx,
		`INSERT INTO source_match_denylist (user_id, provider, external_account_id, external_id, reason)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, provider, external_account_id, external_id)
		 DO UPDATE SET reason = EXCLUDED.reason`,
		e.UserID.UUID(), e.Provider, acct, e.ExternalID, e.Reason,
	)
	if err != nil {
		return fmt.Errorf("add denylist entry: %w", err)
	}
	return nil
}

func (r *SourceDenylistRepo) IsDenied(ctx context.Context, userID domain.UserID, provider string, externalAccountID *domain.ExternalAccountID, externalID string) (bool, error) {
	db := dbtx(ctx, r.pool)
	var acct any
	if externalAccountID != nil {
		acct = externalAccountID.UUID()
	}
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM source_match_denylist
		    WHERE user_id = $1 AND provider = $2
		      AND external_account_id IS NOT DISTINCT FROM $3
		      AND external_id = $4)`,
		userID.UUID(), provider, acct, externalID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check denylist: %w", err)
	}
	return exists, nil
}
