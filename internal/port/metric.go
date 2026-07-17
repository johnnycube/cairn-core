package port

import (
	"context"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// MetricRepo persists and reads `metrics` hypertable rows.
//
// Upsert semantics: rows with non-empty ExternalID conflict-resolve via
// the (user_id, key, ts, provider, external_id) unique index from
// migration 9. Computed metrics (provider = cairn) always set
// ExternalID = "computed:<timestamp>" so reruns of the same compute
// pass overwrite cleanly. Manual entries (ExternalID empty) never
// dedup — each call inserts a fresh row.
type MetricRepo interface {
	// SaveMetrics upserts a batch by the natural key described above.
	// Empty input is a no-op.
	SaveMetrics(ctx context.Context, metrics []domain.Metric) error

	// ListMetricsForUser returns metrics for the user within [start, end],
	// optionally filtered by key (empty matches all). Ordered by ts ASC.
	// The window is inclusive on both ends.
	ListMetricsForUser(
		ctx context.Context,
		userID domain.UserID,
		key string,
		start, end time.Time,
	) ([]domain.Metric, error)

	// DeleteComputedForUser removes every metrics row whose provider is
	// domain.MetricProviderComputed and whose key matches one of the
	// supplied keys, within [start, end]. Used by ComputeTrainingLoad
	// before rewriting the user's rollups so stale rows from prior
	// compute windows don't linger.
	DeleteComputedForUser(
		ctx context.Context,
		userID domain.UserID,
		keys []string,
		start, end time.Time,
	) error
}
