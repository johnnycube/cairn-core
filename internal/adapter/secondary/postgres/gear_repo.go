package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/opencairn-org/cairn-core/internal/domain"
)

// GearRepo implements port.GearRepo over gear, gear_external_refs and
// gear_defaults.
type GearRepo struct{ pool *pgxpool.Pool }

func NewGearRepo(pool *pgxpool.Pool) *GearRepo {
	return &GearRepo{pool: pool}
}

const gearColumns = `
	id, user_id, kind, name, COALESCE(brand, ''), COALESCE(model, ''), COALESCE(notes, ''),
	purchase_date, retired, retired_at,
	total_distance_m, total_moving_s, activity_count,
	created_at, updated_at`

func (r *GearRepo) Get(ctx context.Context, id domain.GearID) (domain.Gear, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx, `SELECT `+gearColumns+` FROM gear WHERE id = $1`, id.UUID())
	g, err := scanGear(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Gear{}, fmt.Errorf("get gear %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Gear{}, fmt.Errorf("get gear %s: %w", id, err)
	}
	refs, err := r.listRefs(ctx, []domain.GearID{id})
	if err != nil {
		return domain.Gear{}, err
	}
	g.ExternalRefs = refs[id]
	return g, nil
}

func (r *GearRepo) ListForUser(ctx context.Context, userID domain.UserID, includeRetired bool) ([]domain.Gear, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+gearColumns+` FROM gear
		  WHERE user_id = $1 AND ($2 OR NOT retired)
		  ORDER BY retired, kind, lower(name), id`,
		userID.UUID(), includeRetired)
	if err != nil {
		return nil, fmt.Errorf("list gear: %w", err)
	}
	defer rows.Close()
	var out []domain.Gear
	var ids []domain.GearID
	for rows.Next() {
		g, err := scanGear(rows)
		if err != nil {
			return nil, fmt.Errorf("scan gear: %w", err)
		}
		out = append(out, g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := r.listRefs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ExternalRefs = refs[out[i].ID]
	}
	return out, nil
}

func (r *GearRepo) Save(ctx context.Context, g domain.Gear) error {
	if err := g.Validate(); err != nil {
		return err
	}
	db := dbtx(ctx, r.pool)
	var purchase any
	if g.PurchaseDate != nil {
		purchase = *g.PurchaseDate
	}
	var retiredAt any
	if g.RetiredAt != nil {
		retiredAt = *g.RetiredAt
	}
	_, err := db.Exec(ctx,
		`INSERT INTO gear (id, user_id, kind, name, brand, model, notes, purchase_date, retired, retired_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO UPDATE SET
		   kind = EXCLUDED.kind, name = EXCLUDED.name, brand = EXCLUDED.brand,
		   model = EXCLUDED.model, notes = EXCLUDED.notes, purchase_date = EXCLUDED.purchase_date,
		   retired = EXCLUDED.retired, retired_at = EXCLUDED.retired_at, updated_at = now()`,
		g.ID.UUID(), g.UserID.UUID(), string(g.Kind), g.Name, g.Brand, g.Model, g.Notes,
		purchase, g.Retired, retiredAt)
	if err != nil {
		return fmt.Errorf("save gear %s: %w", g.ID, err)
	}
	return nil
}

func (r *GearRepo) Delete(ctx context.Context, id domain.GearID) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx, `DELETE FROM gear WHERE id = $1`, id.UUID())
	if err != nil {
		return fmt.Errorf("delete gear %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete gear %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

func (r *GearRepo) UpsertExternalRef(ctx context.Context, ref domain.GearExternalRef) error {
	db := dbtx(ctx, r.pool)
	seen := ref.LastSeenAt
	if seen.IsZero() {
		seen = time.Now().UTC()
	}
	_, err := db.Exec(ctx,
		`INSERT INTO gear_external_refs
		   (gear_id, provider, external_account_id, external_id, provider_name, provider_distance_m, is_primary, last_seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (provider, external_account_id, external_id) DO UPDATE SET
		   gear_id = EXCLUDED.gear_id, provider_name = EXCLUDED.provider_name,
		   provider_distance_m = EXCLUDED.provider_distance_m, is_primary = EXCLUDED.is_primary,
		   last_seen_at = EXCLUDED.last_seen_at`,
		ref.GearID.UUID(), ref.Provider, ref.ExternalAccountID.UUID(), ref.ExternalID,
		ref.ProviderName, ref.ProviderDistanceM, ref.Primary, seen)
	if err != nil {
		return fmt.Errorf("upsert gear ref: %w", err)
	}
	return nil
}

func (r *GearRepo) FindByExternalRef(ctx context.Context, key domain.GearRefKey) (domain.GearID, error) {
	db := dbtx(ctx, r.pool)
	var id uuid.UUID
	err := db.QueryRow(ctx,
		`SELECT gear_id FROM gear_external_refs
		  WHERE provider = $1 AND external_account_id = $2 AND external_id = $3`,
		key.Provider, key.ExternalAccountID.UUID(), key.ExternalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GearID{}, fmt.Errorf("gear ref %s/%s: %w", key.Provider, key.ExternalID, domain.ErrNotFound)
	}
	if err != nil {
		return domain.GearID{}, fmt.Errorf("find gear ref: %w", err)
	}
	return domain.GearID(id), nil
}

func (r *GearRepo) ResolveExternalRefs(ctx context.Context, keys []domain.GearRefKey) (map[domain.GearRefKey]domain.GearID, error) {
	out := make(map[domain.GearRefKey]domain.GearID, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	providers := make([]string, len(keys))
	accounts := make([]uuid.UUID, len(keys))
	externals := make([]string, len(keys))
	for i, k := range keys {
		providers[i], accounts[i], externals[i] = k.Provider, k.ExternalAccountID.UUID(), k.ExternalID
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT r.provider, r.external_account_id, r.external_id, r.gear_id
		   FROM gear_external_refs r
		   JOIN unnest($1::text[], $2::uuid[], $3::text[]) AS k(provider, account_id, external_id)
		     ON r.provider = k.provider AND r.external_account_id = k.account_id AND r.external_id = k.external_id`,
		providers, accounts, externals)
	if err != nil {
		return nil, fmt.Errorf("resolve gear refs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k domain.GearRefKey
		var acct, gid uuid.UUID
		if err := rows.Scan(&k.Provider, &acct, &k.ExternalID, &gid); err != nil {
			return nil, fmt.Errorf("scan gear ref: %w", err)
		}
		k.ExternalAccountID = domain.ExternalAccountID(acct)
		out[k] = domain.GearID(gid)
	}
	return out, rows.Err()
}

func (r *GearRepo) GetDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType) (domain.GearID, error) {
	db := dbtx(ctx, r.pool)
	var id uuid.UUID
	err := db.QueryRow(ctx,
		`SELECT gear_id FROM gear_defaults WHERE user_id = $1 AND activity_type = $2`,
		userID.UUID(), string(t)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GearID{}, fmt.Errorf("gear default for %s: %w", t, domain.ErrNotFound)
	}
	if err != nil {
		return domain.GearID{}, fmt.Errorf("get gear default: %w", err)
	}
	return domain.GearID(id), nil
}

func (r *GearRepo) SetDefault(ctx context.Context, d domain.GearDefault) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO gear_defaults (user_id, activity_type, gear_id) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, activity_type) DO UPDATE SET gear_id = EXCLUDED.gear_id`,
		d.UserID.UUID(), string(d.ActivityType), d.GearID.UUID())
	if err != nil {
		return fmt.Errorf("set gear default: %w", err)
	}
	return nil
}

func (r *GearRepo) ClearDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM gear_defaults WHERE user_id = $1 AND activity_type = $2`,
		userID.UUID(), string(t))
	if err != nil {
		return fmt.Errorf("clear gear default: %w", err)
	}
	return nil
}

func (r *GearRepo) ListDefaults(ctx context.Context, userID domain.UserID) ([]domain.GearDefault, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT activity_type, gear_id FROM gear_defaults WHERE user_id = $1 ORDER BY activity_type`,
		userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list gear defaults: %w", err)
	}
	defer rows.Close()
	var out []domain.GearDefault
	for rows.Next() {
		var t string
		var gid uuid.UUID
		if err := rows.Scan(&t, &gid); err != nil {
			return nil, fmt.Errorf("scan gear default: %w", err)
		}
		out = append(out, domain.GearDefault{UserID: userID, ActivityType: domain.ActivityType(t), GearID: domain.GearID(gid)})
	}
	return out, rows.Err()
}

// Repoint moves refs, defaults and activity attachments from one gear to
// another. Callers run it inside a transaction with the Delete of `from`.
func (r *GearRepo) Repoint(ctx context.Context, from, to domain.GearID) error {
	db := dbtx(ctx, r.pool)
	stmts := []string{
		`UPDATE gear_external_refs SET gear_id = $2 WHERE gear_id = $1`,
		`UPDATE gear_defaults SET gear_id = $2 WHERE gear_id = $1`,
		`UPDATE activities SET gear_id = $2, updated_at = now() WHERE gear_id = $1`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(ctx, q, from.UUID(), to.UUID()); err != nil {
			return fmt.Errorf("repoint gear %s → %s: %w", from, to, err)
		}
	}
	return nil
}

// RecomputeTotalsForUser rewrites the user's gear totals from live
// activities. One statement: gear with no activities is zeroed too.
func (r *GearRepo) RecomputeTotalsForUser(ctx context.Context, userID domain.UserID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE gear g SET
		   total_distance_m = COALESCE(t.distance_m, 0),
		   total_moving_s   = COALESCE(t.moving_s, 0),
		   activity_count   = COALESCE(t.n, 0),
		   updated_at       = now()
		 FROM gear g2
		 LEFT JOIN (
		   SELECT gear_id,
		          sum(COALESCE(distance_m, 0))   AS distance_m,
		          sum(moving_duration_s)         AS moving_s,
		          count(*)                       AS n
		     FROM activities
		    WHERE user_id = $1 AND deleted_at IS NULL AND gear_id IS NOT NULL
		    GROUP BY gear_id
		 ) t ON t.gear_id = g2.id
		 WHERE g.id = g2.id AND g2.user_id = $1
		   AND (g.total_distance_m IS DISTINCT FROM COALESCE(t.distance_m, 0)
		     OR g.total_moving_s IS DISTINCT FROM COALESCE(t.moving_s, 0)
		     OR g.activity_count IS DISTINCT FROM COALESCE(t.n, 0))`,
		userID.UUID())
	if err != nil {
		return fmt.Errorf("recompute gear totals: %w", err)
	}
	return nil
}

func (r *GearRepo) ListUserIDsWithGear(ctx context.Context) ([]domain.UserID, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx, `SELECT DISTINCT user_id FROM gear`)
	if err != nil {
		return nil, fmt.Errorf("list gear owners: %w", err)
	}
	defer rows.Close()
	var out []domain.UserID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, domain.UserID(id))
	}
	return out, rows.Err()
}

// listRefs loads the external refs of many gear in one query.
func (r *GearRepo) listRefs(ctx context.Context, ids []domain.GearID) (map[domain.GearID][]domain.GearExternalRef, error) {
	out := map[domain.GearID][]domain.GearExternalRef{}
	if len(ids) == 0 {
		return out, nil
	}
	raw := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		raw[i] = id.UUID()
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT gear_id, provider, external_account_id, external_id, provider_name,
		        provider_distance_m, is_primary, last_seen_at
		   FROM gear_external_refs WHERE gear_id = ANY($1)
		  ORDER BY provider, external_id`, raw)
	if err != nil {
		return nil, fmt.Errorf("list gear refs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref domain.GearExternalRef
		var gid, acct uuid.UUID
		if err := rows.Scan(&gid, &ref.Provider, &acct, &ref.ExternalID, &ref.ProviderName,
			&ref.ProviderDistanceM, &ref.Primary, &ref.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan gear ref: %w", err)
		}
		ref.GearID = domain.GearID(gid)
		ref.ExternalAccountID = domain.ExternalAccountID(acct)
		out[ref.GearID] = append(out[ref.GearID], ref)
	}
	return out, rows.Err()
}

func scanGear(row rowScanner) (domain.Gear, error) {
	var (
		g            domain.Gear
		id, userID   uuid.UUID
		kind         string
		purchase     *time.Time
		totalMovingS int64
	)
	err := row.Scan(
		&id, &userID, &kind, &g.Name, &g.Brand, &g.Model, &g.Notes,
		&purchase, &g.Retired, &g.RetiredAt,
		&g.TotalDistanceM, &totalMovingS, &g.ActivityCount,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return domain.Gear{}, err
	}
	g.ID = domain.GearID(id)
	g.UserID = domain.UserID(userID)
	g.Kind = domain.GearKind(kind)
	g.PurchaseDate = purchase
	g.TotalMoving = time.Duration(totalMovingS) * time.Second
	return g, nil
}
