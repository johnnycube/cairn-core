package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// MetricRepo implements port.MetricRepo on the `metrics` hypertable.
//
// Two distinct write patterns:
//
//  1. Computed metrics (training_load.*, derived FTP, ...) carry
//     provider="cairn" and a deterministic external_id like
//     "computed:2026-05-01". UPSERT replaces the prior value via the
//     partial unique index from migration 9.
//
//  2. Imported metrics (workers carrying weight, HRV, etc.) carry their
//     provider's external_id. Reimports of the same provider value
//     dedup via the same index.
type MetricRepo struct {
	pool *pgxpool.Pool
}

func NewMetricRepo(pool *pgxpool.Pool) *MetricRepo {
	return &MetricRepo{pool: pool}
}

const metricColumns = `
	id, user_id, key, ts, period_seconds,
	value_numeric, value_struct,
	provider, external_account_id, external_id,
	source_worker_name, source_worker_version,
	activity_id, tags, notes,
	created_at, updated_at
`

// SaveMetrics UPSERTs each metric. The conflict target matches the
// partial unique index `metrics_natural_key_uniq`:
//
//	(user_id, key, ts, provider, external_id) WHERE external_id IS NOT NULL
//
// Rows with empty ExternalID never trip the constraint and always insert
// fresh; this matches the schema convention that manual entries are
// always new (no idempotent rewrite path).
func (r *MetricRepo) SaveMetrics(ctx context.Context, metrics []domain.Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	db := dbtx(ctx, r.pool)

	batch := &pgx.Batch{}
	for _, m := range metrics {
		var (
			extAcctParam  any
			activityParam any
			structParam   any
		)
		if m.ExternalAccountID != nil {
			extAcctParam = m.ExternalAccountID.UUID()
		}
		if m.ActivityID != nil {
			activityParam = m.ActivityID.UUID()
		}
		if len(m.ValueStruct) > 0 {
			structParam = []byte(m.ValueStruct)
		}
		// metrics.tags is NOT NULL; a nil Go slice binds as SQL NULL and
		// violates the constraint. Coalesce to an empty array.
		tags := m.Tags
		if tags == nil {
			tags = []string{}
		}

		// ON CONFLICT on a partial unique index requires repeating the
		// WHERE clause so Postgres knows which index to use.
		batch.Queue(`
			INSERT INTO metrics (
				id, user_id, key, ts, period_seconds,
				value_numeric, value_struct,
				provider, external_account_id, external_id,
				source_worker_name, source_worker_version,
				activity_id, tags, notes,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7,
				$8, $9, NULLIF($10, ''),
				$11, $12,
				$13, $14, $15,
				COALESCE($16, now()), COALESCE($17, now())
			)
			ON CONFLICT (user_id, key, ts, provider, external_id) WHERE external_id IS NOT NULL
			DO UPDATE SET
				period_seconds       = EXCLUDED.period_seconds,
				value_numeric        = EXCLUDED.value_numeric,
				value_struct         = EXCLUDED.value_struct,
				external_account_id  = EXCLUDED.external_account_id,
				source_worker_name   = EXCLUDED.source_worker_name,
				source_worker_version = EXCLUDED.source_worker_version,
				activity_id          = EXCLUDED.activity_id,
				tags                 = EXCLUDED.tags,
				notes                = EXCLUDED.notes
			`,
			m.ID.UUID(), m.UserID.UUID(), m.Key, m.Timestamp, m.PeriodSeconds,
			m.ValueNumeric, structParam,
			m.Provider, extAcctParam, m.ExternalID,
			m.SourceWorkerName, m.SourceWorkerVersion,
			activityParam, tags, m.Notes,
			nullableTime(m.CreatedAt), nullableTime(m.UpdatedAt),
		)
	}

	br := db.(pgxBatchExecer).SendBatch(ctx, batch)
	defer br.Close()
	for i := range metrics {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("save metric %d: %w", i, err)
		}
	}
	return nil
}

// ListMetricsForUser returns metrics for the user. Empty key matches all.
func (r *MetricRepo) ListMetricsForUser(
	ctx context.Context,
	userID domain.UserID,
	key string,
	start, end time.Time,
) ([]domain.Metric, error) {
	db := dbtx(ctx, r.pool)

	args := []any{userID.UUID(), start, end}
	q := `SELECT ` + metricColumns + `
	        FROM metrics
	       WHERE user_id = $1
	         AND ts BETWEEN $2 AND $3`
	if key != "" {
		args = append(args, key)
		q += fmt.Sprintf(` AND key = $%d`, len(args))
	}
	q += ` ORDER BY ts ASC`

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list metrics for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []domain.Metric
	for rows.Next() {
		m, err := scanMetricRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan metric row: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metrics: %w", err)
	}
	return out, nil
}

// DeleteComputedForUser removes provider='cairn' rows for the given keys
// within the time window. ANY-in-array filter so one statement covers
// the full key set.
func (r *MetricRepo) DeleteComputedForUser(
	ctx context.Context,
	userID domain.UserID,
	keys []string,
	start, end time.Time,
) error {
	if len(keys) == 0 {
		return nil
	}
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM metrics
		  WHERE user_id = $1
		    AND provider = $2
		    AND key = ANY($3)
		    AND ts BETWEEN $4 AND $5`,
		userID.UUID(), domain.MetricProviderComputed, keys, start, end,
	)
	if err != nil {
		return fmt.Errorf("delete computed metrics for %s: %w", userID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scan helper
// ---------------------------------------------------------------------------

func scanMetricRow(row rowScanner) (domain.Metric, error) {
	var (
		id, userID                            uuid.UUID
		key                                   string
		ts, createdAt, updatedAt              time.Time
		periodSeconds                         int
		valueNumeric                          *float64
		valueStruct                           []byte
		provider                              string
		externalAccountID                     *uuid.UUID
		externalID                            *string
		sourceWorkerName, sourceWorkerVersion *string
		activityID                            *uuid.UUID
		tags                                  []string
		notes                                 string
	)
	if err := row.Scan(
		&id, &userID, &key, &ts, &periodSeconds,
		&valueNumeric, &valueStruct,
		&provider, &externalAccountID, &externalID,
		&sourceWorkerName, &sourceWorkerVersion,
		&activityID, &tags, &notes,
		&createdAt, &updatedAt,
	); err != nil {
		return domain.Metric{}, err
	}

	m := domain.Metric{
		ID:            domain.MetricID(id),
		UserID:        domain.UserID(userID),
		Key:           key,
		Timestamp:     ts,
		PeriodSeconds: periodSeconds,
		ValueNumeric:  valueNumeric,
		Provider:      provider,
		Tags:          tags,
		Notes:         notes,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
	if len(valueStruct) > 0 {
		m.ValueStruct = json.RawMessage(valueStruct)
	}
	if externalAccountID != nil {
		ea := domain.ExternalAccountID(*externalAccountID)
		m.ExternalAccountID = &ea
	}
	if externalID != nil {
		m.ExternalID = *externalID
	}
	if sourceWorkerName != nil {
		m.SourceWorkerName = *sourceWorkerName
	}
	if sourceWorkerVersion != nil {
		m.SourceWorkerVersion = *sourceWorkerVersion
	}
	if activityID != nil {
		a := domain.ActivityID(*activityID)
		m.ActivityID = &a
	}
	return m, nil
}
