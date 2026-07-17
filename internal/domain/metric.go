package domain

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Metric
//
// Generic time-series data point. Mirrors the `metrics` hypertable. One
// row per (user, key, ts, provider, external_id). Values are either
// scalar (ValueNumeric) or structured (ValueStruct); exactly one is set.
//
// Common keys:
//   - "training_load.tss"        — daily TSS rollup (sum of activity TSS)
//   - "training_load.ctl"        — chronic training load (42-day EWMA)
//   - "training_load.atl"        — acute training load (7-day EWMA)
//   - "training_load.tsb"        — training stress balance (CTL - ATL)
//   - "body.weight_kg"           — body weight
//   - "body.resting_hr_bpm"      — resting heart rate
//   - "threshold.ftp_w"          — auto-derived FTP (overridden via
//                                  user_settings.ftp_cycling_override_w)
// ---------------------------------------------------------------------------

type Metric struct {
	ID            MetricID
	UserID        UserID
	Key           string
	Timestamp     time.Time
	PeriodSeconds int // 0 for instantaneous; 86400 for daily rollups

	// Exactly one of these is non-nil. ValueStruct uses json.RawMessage
	// so the caller controls the wire shape per metric key.
	ValueNumeric *float64
	ValueStruct  json.RawMessage

	// Provenance — who recorded this metric.
	Provider            string // "cairn" for system-computed; provider name otherwise
	ExternalAccountID   *ExternalAccountID
	ExternalID          string // dedup key when set; empty for manual entries
	SourceWorkerName    string
	SourceWorkerVersion string

	// Activity link — set for activity-derived metrics (per-ride TSS, etc.)
	// so the UI can hyperlink from the metric to the source activity.
	ActivityID *ActivityID

	Tags  []string
	Notes string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ---------------------------------------------------------------------------
// Named metric keys
// ---------------------------------------------------------------------------

// Training-load metric keys. The first four are computed by the
// usecase/trainingload package and re-emitted on every activity ingest.
const (
	MetricKeyTrainingLoadTSS = "training_load.tss"
	MetricKeyTrainingLoadCTL = "training_load.ctl"
	MetricKeyTrainingLoadATL = "training_load.atl"
	MetricKeyTrainingLoadTSB = "training_load.tsb"
)

// AllTrainingLoadKeys is the iteration order used by recompute and by
// the DELETE-then-INSERT branch in the postgres adapter.
var AllTrainingLoadKeys = []string{
	MetricKeyTrainingLoadTSS,
	MetricKeyTrainingLoadCTL,
	MetricKeyTrainingLoadATL,
	MetricKeyTrainingLoadTSB,
}

// MetricProviderComputed is the provider tag the application uses for
// metrics it computes itself (training load, derived FTP, etc.). Workers
// that import metrics from third parties use their own provider tag
// ("strava", "garmin", "withings", ...).
const MetricProviderComputed = "cairn"
