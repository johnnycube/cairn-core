package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// UserSettingsRepo implements port.UserSettingsRepo.
type UserSettingsRepo struct {
	pool *pgxpool.Pool
}

// NewUserSettingsRepo wires the repository onto an existing pool.
func NewUserSettingsRepo(pool *pgxpool.Pool) *UserSettingsRepo {
	return &UserSettingsRepo{pool: pool}
}

// mergePolicyJSON is the per-activity-type entry inside user_settings.
// merge_policy_by_activity_type.
type mergePolicyJSON struct {
	DefaultPriority []string            `json:"default_priority"`
	Overrides       map[string][]string `json:"overrides,omitempty"`
}

func (j mergePolicyJSON) toDomain() domain.MergePolicy {
	out := domain.MergePolicy{DefaultPriority: j.DefaultPriority}
	if len(j.Overrides) > 0 {
		out.Overrides = make(map[domain.FieldGroup][]string, len(j.Overrides))
		for k, v := range j.Overrides {
			out.Overrides[domain.FieldGroup(k)] = v
		}
	}
	return out
}

func mergePolicyJSONFrom(p domain.MergePolicy) mergePolicyJSON {
	j := mergePolicyJSON{DefaultPriority: p.DefaultPriority}
	if len(p.Overrides) > 0 {
		j.Overrides = make(map[string][]string, len(p.Overrides))
		for k, v := range p.Overrides {
			j.Overrides[string(k)] = v
		}
	}
	return j
}

func policiesToJSON(policies map[domain.ActivityType]domain.MergePolicy) (map[string]mergePolicyJSON, error) {
	out := make(map[string]mergePolicyJSON, len(policies))
	for t, p := range policies {
		if len(p.DefaultPriority) == 0 && len(p.Overrides) == 0 {
			continue
		}
		out[string(t)] = mergePolicyJSONFrom(p)
	}
	return out, nil
}

func policiesFromJSON(raw []byte) (map[domain.ActivityType]domain.MergePolicy, error) {
	out := map[domain.ActivityType]domain.MergePolicy{}
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return out, nil
	}
	var all map[string]mergePolicyJSON
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("unmarshal merge policies: %w", err)
	}
	for k, v := range all {
		if len(v.DefaultPriority) == 0 && len(v.Overrides) == 0 {
			continue
		}
		out[domain.ActivityType(k)] = v.toDomain()
	}
	return out, nil
}

// GetMergePolicy returns the user's policy for activityType. Behaviour:
//
//   - No user_settings row for this user             → DefaultMergePolicyFor(t)
//   - Row exists but no entry for this activityType  → DefaultMergePolicyFor(t)
//   - Row + entry exists                             → user's policy
//
// All three branches return nil for the error — the caller (the policy
// resolver in the merge engine) does not distinguish between "user has
// no override" and "user has default override".
func (r *UserSettingsRepo) GetMergePolicy(
	ctx context.Context,
	userID domain.UserID,
	activityType domain.ActivityType,
) (domain.MergePolicy, error) {
	db := dbtx(ctx, r.pool)

	var raw []byte
	err := db.QueryRow(ctx,
		`SELECT merge_policy_by_activity_type FROM user_settings WHERE user_id = $1`,
		userID.UUID(),
	).Scan(&raw)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DefaultMergePolicyFor(activityType), nil
	}
	if err != nil {
		return domain.MergePolicy{}, fmt.Errorf("read user_settings for %s: %w", userID, err)
	}

	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return domain.DefaultMergePolicyFor(activityType), nil
	}

	var all map[string]mergePolicyJSON
	if err := json.Unmarshal(raw, &all); err != nil {
		return domain.MergePolicy{}, fmt.Errorf("unmarshal merge_policy_by_activity_type: %w", err)
	}

	entry, ok := all[string(activityType)]
	if !ok || len(entry.DefaultPriority) == 0 {
		return r.instanceDefaultOr(ctx, activityType), nil
	}

	return entry.toDomain(), nil
}

// instanceDefaultOr returns the instance-wide default policy for the type, or
// domain.DefaultMergePolicyFor when none is configured. The middle level of the
// cascade (brief §5).
func (r *UserSettingsRepo) instanceDefaultOr(ctx context.Context, activityType domain.ActivityType) domain.MergePolicy {
	defaults, err := r.GetInstanceMergeDefaults(ctx)
	if err == nil {
		if p, ok := defaults[activityType]; ok && len(p.DefaultPriority) > 0 {
			return p
		}
	}
	return domain.DefaultMergePolicyFor(activityType)
}

// GetAllMergePolicies returns the user's explicitly-configured policies.
func (r *UserSettingsRepo) GetAllMergePolicies(ctx context.Context, userID domain.UserID) (map[domain.ActivityType]domain.MergePolicy, error) {
	db := dbtx(ctx, r.pool)
	var raw []byte
	err := db.QueryRow(ctx,
		`SELECT merge_policy_by_activity_type FROM user_settings WHERE user_id = $1`,
		userID.UUID(),
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[domain.ActivityType]domain.MergePolicy{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user_settings: %w", err)
	}
	return policiesFromJSON(raw)
}

// SetMergePolicies replaces the user's full per-type policy map (upsert).
func (r *UserSettingsRepo) SetMergePolicies(ctx context.Context, userID domain.UserID, policies map[domain.ActivityType]domain.MergePolicy) error {
	db := dbtx(ctx, r.pool)
	j, _ := policiesToJSON(policies)
	raw, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("marshal merge policies: %w", err)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO user_settings (user_id, merge_policy_by_activity_type)
		 VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET merge_policy_by_activity_type = EXCLUDED.merge_policy_by_activity_type`,
		userID.UUID(), raw,
	)
	if err != nil {
		return fmt.Errorf("set merge policies: %w", err)
	}
	return nil
}

// GetInstanceMergeDefaults reads the instance-wide defaults.
func (r *UserSettingsRepo) GetInstanceMergeDefaults(ctx context.Context) (map[domain.ActivityType]domain.MergePolicy, error) {
	db := dbtx(ctx, r.pool)
	var raw []byte
	err := db.QueryRow(ctx, `SELECT merge_defaults_json FROM instance_settings WHERE id = 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[domain.ActivityType]domain.MergePolicy{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read instance merge defaults: %w", err)
	}
	return policiesFromJSON(raw)
}

// SetInstanceMergeDefaults replaces the instance-wide defaults.
func (r *UserSettingsRepo) SetInstanceMergeDefaults(ctx context.Context, policies map[domain.ActivityType]domain.MergePolicy) error {
	db := dbtx(ctx, r.pool)
	j, _ := policiesToJSON(policies)
	raw, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("marshal instance merge defaults: %w", err)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO instance_settings (id, merge_defaults_json) VALUES (1, $1)
		 ON CONFLICT (id) DO UPDATE SET merge_defaults_json = EXCLUDED.merge_defaults_json`,
		raw,
	)
	if err != nil {
		return fmt.Errorf("set instance merge defaults: %w", err)
	}
	return nil
}
