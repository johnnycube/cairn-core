package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// StreamRepo implements port.StreamRepo on top of the activity_streams
// hypertable and its 5s/30s continuous aggregates (migration 6).
//
// WriteStream uses pgx.CopyFrom for bulk inserts — typical activities have
// 1k-10k samples per source, and individual INSERTs would be 50-100×
// slower than CopyFrom over a single round trip.
type StreamRepo struct {
	pool *pgxpool.Pool
}

func NewStreamRepo(pool *pgxpool.Pool) *StreamRepo {
	return &StreamRepo{pool: pool}
}

// ---------------------------------------------------------------------------
// Bulk copy helper interface
//
// DBTX (the one in dbtx.go) intentionally exposes only the Exec/Query/QueryRow
// surface used by most repositories. CopyFrom is StreamRepo-specific, so we
// reach for it via a local copier interface that both pgx.Tx and
// *pgxpool.Pool satisfy.
// ---------------------------------------------------------------------------

type copier interface {
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// dbCopier resolves to the active transaction's copier from ctx, or the
// pool when no transaction is active.
func (r *StreamRepo) dbCopier(ctx context.Context) copier {
	if tx, ok := txFromCtx(ctx); ok {
		return tx
	}
	return r.pool
}

// ---------------------------------------------------------------------------
// WriteStream
// ---------------------------------------------------------------------------

// streamRawColumns is the column list used for raw inserts. Order matches
// the streamRowSource below — keep them in sync.
var streamRawColumns = []string{
	"activity_source_id", "ts",
	"latitude", "longitude", "altitude_m", "distance_m", "speed_mps",
	"heart_rate_bpm", "power_w", "cadence", "temperature_c", "grade",
	"left_right_balance", "left_torque_effectiveness", "right_torque_effectiveness",
	"left_pedal_smoothness", "right_pedal_smoothness",
	"vertical_oscillation_mm", "ground_contact_time_ms", "stride_length_m",
	"respiration_rate_brpm", "core_temperature_c",
}

// WriteStream replaces all samples for the source. Atomic — DELETE and
// COPY run in the same transaction (either an outer InTx caller's or a
// short auto-commit pair).
func (r *StreamRepo) WriteStream(ctx context.Context, sourceID domain.SourceID, samples []domain.StreamSample) error {
	db := r.dbCopier(ctx)

	// DELETE always runs, even when samples is empty — empty input means
	// "clear the stream", which is the right behaviour on source detach.
	if _, err := db.Exec(ctx,
		`DELETE FROM activity_streams WHERE activity_source_id = $1`,
		sourceID.UUID(),
	); err != nil {
		return fmt.Errorf("delete existing stream for %s: %w", sourceID, err)
	}

	if len(samples) == 0 {
		return nil
	}

	// Provider data can contain duplicate timestamps (seen live: a Zwift
	// ride synced to Garmin repeated 3 of 1977 samples around a pause).
	// The table's (source, ts) primary key makes that a COPY failure that
	// poisons the whole ingest, so collapse duplicates here — last sample
	// wins, order preserved.
	samples = dedupeSamplesByTimestamp(samples)

	src := &streamRowSource{sourceID: sourceID, samples: samples}
	if _, err := db.CopyFrom(ctx,
		pgx.Identifier{"activity_streams"},
		streamRawColumns,
		src,
	); err != nil {
		return fmt.Errorf("copy stream samples for %s: %w", sourceID, err)
	}
	return nil
}

// dedupeSamplesByTimestamp collapses samples sharing a timestamp to the
// last occurrence, preserving the original order otherwise. Returns the
// input slice unchanged when there are no duplicates (the common case).
func dedupeSamplesByTimestamp(samples []domain.StreamSample) []domain.StreamSample {
	seen := make(map[int64]int, len(samples)) // unix-nano ts → index in out
	out := samples[:0:0]
	for _, s := range samples {
		key := s.Timestamp.UnixNano()
		if i, dup := seen[key]; dup {
			out[i] = s
			continue
		}
		seen[key] = len(out)
		out = append(out, s)
	}
	if len(out) == len(samples) {
		return samples
	}
	return out
}

// streamRowSource is the pgx.CopyFromSource adapter for a slice of
// domain.StreamSample. Each Next/Values pair yields one row in the order
// declared by streamRawColumns.
type streamRowSource struct {
	sourceID domain.SourceID
	samples  []domain.StreamSample
	idx      int
}

func (s *streamRowSource) Next() bool { s.idx++; return s.idx <= len(s.samples) }
func (s *streamRowSource) Err() error { return nil }
func (s *streamRowSource) Values() ([]any, error) {
	sample := s.samples[s.idx-1]
	return []any{
		s.sourceID.UUID(), sample.Timestamp,
		sample.Latitude, sample.Longitude, sample.AltitudeM, sample.DistanceM, sample.SpeedMps,
		sample.HeartRateBpm, sample.PowerW, sample.Cadence, sample.TemperatureC, sample.Grade,
		sample.LeftRightBalance, sample.LeftTorqueEffectiveness, sample.RightTorqueEffectiveness,
		sample.LeftPedalSmoothness, sample.RightPedalSmoothness,
		sample.VerticalOscillationMm, sample.GroundContactTimeMs, sample.StrideLengthM,
		sample.RespirationRateBrpm, sample.CoreTemperatureC,
	}, nil
}

// ---------------------------------------------------------------------------
// QueryStream
//
// The chosen resolution decides which table the SELECT hits and which
// channels are populated on the returned StreamSample. Channels not
// stored at the chosen resolution remain nil.
// ---------------------------------------------------------------------------

func (r *StreamRepo) QueryStream(ctx context.Context, q domain.StreamQuery) (domain.Stream, error) {
	switch q.Resolution {
	case "", domain.StreamResolutionRaw:
		return r.queryRaw(ctx, q)
	case domain.StreamResolution5s:
		return r.queryAggregate(ctx, q, "activity_streams_5s", streamCols5s, scan5sSample)
	case domain.StreamResolution30s:
		return r.queryAggregate(ctx, q, "activity_streams_30s", streamCols30s, scan30sSample)
	}
	return domain.Stream{}, fmt.Errorf("unsupported stream resolution: %q", q.Resolution)
}

// timeFilters appends optional StartTime/EndTime predicates and returns
// the args + the appended WHERE fragment.
func timeFilters(args []any, tsColumn string, q domain.StreamQuery) ([]any, string) {
	where := ""
	if !q.StartTime.IsZero() {
		args = append(args, q.StartTime)
		where += fmt.Sprintf(" AND %s >= $%d", tsColumn, len(args))
	}
	if !q.EndTime.IsZero() {
		args = append(args, q.EndTime)
		where += fmt.Sprintf(" AND %s <= $%d", tsColumn, len(args))
	}
	return args, where
}

// queryRaw reads from the activity_streams hypertable, including every
// channel the schema stores.
func (r *StreamRepo) queryRaw(ctx context.Context, q domain.StreamQuery) (domain.Stream, error) {
	db := dbtx(ctx, r.pool)

	args := []any{q.ActivitySourceID.UUID()}
	args, where := timeFilters(args, "ts", q)

	sql := `SELECT
		ts,
		latitude, longitude, altitude_m, distance_m, speed_mps,
		heart_rate_bpm, power_w, cadence, temperature_c, grade,
		left_right_balance, left_torque_effectiveness, right_torque_effectiveness,
		left_pedal_smoothness, right_pedal_smoothness,
		vertical_oscillation_mm, ground_contact_time_ms, stride_length_m,
		respiration_rate_brpm, core_temperature_c
	FROM activity_streams
	WHERE activity_source_id = $1` + where + `
	ORDER BY ts ASC`

	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return domain.Stream{}, fmt.Errorf("query raw stream for %s: %w", q.ActivitySourceID, err)
	}
	defer rows.Close()

	var samples []domain.StreamSample
	for rows.Next() {
		var s domain.StreamSample
		if err := scanRawSample(rows, &s); err != nil {
			return domain.Stream{}, fmt.Errorf("scan raw sample: %w", err)
		}
		samples = append(samples, s)
	}
	if err := rows.Err(); err != nil {
		return domain.Stream{}, fmt.Errorf("iterate raw stream: %w", err)
	}

	return domain.Stream{
		ActivitySourceID: q.ActivitySourceID,
		Resolution:       domain.StreamResolutionRaw,
		Samples:          samples,
	}, nil
}

// streamCols5s lists the columns selected from activity_streams_5s.
// The continuous aggregate does NOT include the advanced cycling
// metrics (left/right balance, pedal smoothness, torque effectiveness)
// — they're too spiky at 5s averaging to be informative.
var streamCols5s = []string{
	"bucket",
	"latitude", "longitude", "altitude_m", "distance_m", "speed_mps",
	"heart_rate_bpm", "power_w", "cadence", "temperature_c", "grade",
	"vertical_oscillation_mm", "ground_contact_time_ms", "stride_length_m",
	"respiration_rate_brpm", "core_temperature_c",
}

// streamCols30s lists the columns selected from activity_streams_30s.
// Drops the running-specific advanced metrics too — at 30s those are
// not useful, and the chart pass focuses on core scalars.
var streamCols30s = []string{
	"bucket",
	"latitude", "longitude", "altitude_m", "distance_m", "speed_mps",
	"heart_rate_bpm", "power_w", "cadence", "temperature_c", "grade",
}

type aggregateScanner func(rowScanner, *domain.StreamSample) error

func (r *StreamRepo) queryAggregate(
	ctx context.Context,
	q domain.StreamQuery,
	table string,
	cols []string,
	scan aggregateScanner,
) (domain.Stream, error) {
	db := dbtx(ctx, r.pool)

	args := []any{q.ActivitySourceID.UUID()}
	args, where := timeFilters(args, "bucket", q)

	// table name is from a tightly-scoped constant set (5s / 30s); not user input.
	sql := `SELECT ` + joinCols(cols) + `
	FROM ` + table + `
	WHERE activity_source_id = $1` + where + `
	ORDER BY bucket ASC`

	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return domain.Stream{}, fmt.Errorf("query %s stream for %s: %w", table, q.ActivitySourceID, err)
	}
	defer rows.Close()

	var samples []domain.StreamSample
	for rows.Next() {
		var s domain.StreamSample
		if err := scan(rows, &s); err != nil {
			return domain.Stream{}, fmt.Errorf("scan %s sample: %w", table, err)
		}
		samples = append(samples, s)
	}
	if err := rows.Err(); err != nil {
		return domain.Stream{}, fmt.Errorf("iterate %s stream: %w", table, err)
	}

	return domain.Stream{
		ActivitySourceID: q.ActivitySourceID,
		Resolution:       q.Resolution,
		Samples:          samples,
	}, nil
}

func joinCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

// ---------------------------------------------------------------------------
// scan helpers
// ---------------------------------------------------------------------------

func scanRawSample(row rowScanner, s *domain.StreamSample) error {
	return row.Scan(
		&s.Timestamp,
		&s.Latitude, &s.Longitude, &s.AltitudeM, &s.DistanceM, &s.SpeedMps,
		&s.HeartRateBpm, &s.PowerW, &s.Cadence, &s.TemperatureC, &s.Grade,
		&s.LeftRightBalance, &s.LeftTorqueEffectiveness, &s.RightTorqueEffectiveness,
		&s.LeftPedalSmoothness, &s.RightPedalSmoothness,
		&s.VerticalOscillationMm, &s.GroundContactTimeMs, &s.StrideLengthM,
		&s.RespirationRateBrpm, &s.CoreTemperatureC,
	)
}

func scan5sSample(row rowScanner, s *domain.StreamSample) error {
	return row.Scan(
		&s.Timestamp,
		&s.Latitude, &s.Longitude, &s.AltitudeM, &s.DistanceM, &s.SpeedMps,
		&s.HeartRateBpm, &s.PowerW, &s.Cadence, &s.TemperatureC, &s.Grade,
		&s.VerticalOscillationMm, &s.GroundContactTimeMs, &s.StrideLengthM,
		&s.RespirationRateBrpm, &s.CoreTemperatureC,
	)
}

func scan30sSample(row rowScanner, s *domain.StreamSample) error {
	return row.Scan(
		&s.Timestamp,
		&s.Latitude, &s.Longitude, &s.AltitudeM, &s.DistanceM, &s.SpeedMps,
		&s.HeartRateBpm, &s.PowerW, &s.Cadence, &s.TemperatureC, &s.Grade,
	)
}

// ---------------------------------------------------------------------------
// DeleteStream
// ---------------------------------------------------------------------------

// DeleteStream removes all samples for the source. No-op when the source
// has no stored stream — TimescaleDB's chunk-aware DELETE handles this
// efficiently regardless of dataset size.
func (r *StreamRepo) DeleteStream(ctx context.Context, sourceID domain.SourceID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM activity_streams WHERE activity_source_id = $1`,
		sourceID.UUID(),
	)
	if err != nil {
		return fmt.Errorf("delete stream for %s: %w", sourceID, err)
	}
	return nil
}

// RefreshAggregates materialises the 5s/30s CAggs over the source's own time
// window. refresh_continuous_aggregate cannot run inside a transaction, so we
// use the simple protocol (autocommit). A "concurrent refresh" error means the
// recent-window policy is already covering this range — safe to ignore.
func (r *StreamRepo) RefreshAggregates(ctx context.Context, sourceID domain.SourceID) error {
	var minTS, maxTS *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT min(ts), max(ts) FROM activity_streams WHERE activity_source_id = $1`,
		sourceID.UUID()).Scan(&minTS, &maxTS)
	if err != nil {
		return fmt.Errorf("stream range for %s: %w", sourceID, err)
	}
	if minTS == nil || maxTS == nil {
		return nil // no stream samples
	}
	// Pad past the bounds so the (exclusive) end and bucket alignment cover the
	// first/last samples. Timestamps are server-side → safe to format literally.
	start := minTS.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05-07")
	end := maxTS.Add(time.Minute).UTC().Format("2006-01-02 15:04:05-07")
	for _, view := range []string{"activity_streams_5s", "activity_streams_30s"} {
		sql := fmt.Sprintf("CALL refresh_continuous_aggregate('%s', '%s', '%s')", view, start, end)
		if _, err := r.pool.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			if strings.Contains(err.Error(), "concurrent refresh") {
				continue
			}
			return fmt.Errorf("refresh %s for %s: %w", view, sourceID, err)
		}
	}
	return nil
}

// FirstGeoPoint returns the earliest GPS sample for a source. Reads the raw
// hypertable (not the downsampled aggregates) so it works regardless of
// continuous-aggregate materialisation state. found is false when the source
// has no GPS-bearing samples.
func (r *StreamRepo) FirstGeoPoint(ctx context.Context, sourceID domain.SourceID) (lat, lon float64, found bool, err error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT latitude, longitude
		   FROM activity_streams
		  WHERE activity_source_id = $1
		    AND latitude IS NOT NULL AND longitude IS NOT NULL
		  ORDER BY ts ASC
		  LIMIT 1`,
		sourceID.UUID(),
	)
	if err := row.Scan(&lat, &lon); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("first geo point for %s: %w", sourceID, err)
	}
	return lat, lon, true, nil
}
