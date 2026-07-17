package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// ActivityRepo persists the Activity aggregate (activities + activity_sources).
type ActivityRepo struct {
	pool *pgxpool.Pool
}

// NewActivityRepo wires the repository onto an existing pool.
func NewActivityRepo(pool *pgxpool.Pool) *ActivityRepo {
	return &ActivityRepo{pool: pool}
}

// ---------------------------------------------------------------------------
// Activity (the merged row)
// ---------------------------------------------------------------------------

// activityColumns lists every column on the activities table. The order
// is fixed: scanActivityRow relies on it.
const activityColumns = `
	id, user_id,
	type, discipline, is_virtual, is_ebike, is_commute, is_race, custom_subtype,
	title, description,
	start_time, end_time, elapsed_duration_s, moving_duration_s, timezone,
	distance_m, elevation_gain_m, elevation_loss_m, min_elevation_m, max_elevation_m,
	avg_speed_mps, max_speed_mps,
	avg_heart_rate_bpm, max_heart_rate_bpm,
	avg_power_w, max_power_w, normalized_power_w,
	avg_cadence, max_cadence,
	avg_temperature_c, min_temperature_c, max_temperature_c,
	calories_kcal, tss, intensity_factor,
	pool_length_m, total_strokes,
	merge_provenance,
	primary_stream_source_id,
	merged_at,
	gear_id, tags, privacy,
	created_at, updated_at, deleted_at,
	start_lat, start_lng, start_place,
	hidden_by_admin
`

// GetActivity reads one activities row by ID.
func (r *ActivityRepo) GetActivity(ctx context.Context, id domain.ActivityID) (domain.Activity, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+activityColumns+` FROM activities WHERE id = $1`,
		id.UUID(),
	)
	a, err := scanActivityRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Activity{}, fmt.Errorf("get activity %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Activity{}, fmt.Errorf("get activity %s: %w", id, err)
	}
	return a, nil
}

// SaveActivity upserts an activities row by ID. Callers in the same
// transaction must also persist any source referenced by
// PrimaryStreamSourceID — the FK is DEFERRABLE INITIALLY DEFERRED so
// order within the transaction does not matter; validity is checked at
// commit.
func (r *ActivityRepo) SaveActivity(ctx context.Context, a domain.Activity) error {
	db := dbtx(ctx, r.pool)

	provenanceJSON, err := encodeProvenance(a.MergeProvenance)
	if err != nil {
		return fmt.Errorf("encode merge_provenance: %w", err)
	}

	var disciplineParam any
	if a.Discipline != "" && a.Discipline != domain.DisciplineNone {
		disciplineParam = string(a.Discipline)
	}

	var primaryStreamParam any
	if a.PrimaryStreamSourceID != nil {
		primaryStreamParam = a.PrimaryStreamSourceID.UUID()
	}

	var gearParam any
	if a.GearID != nil {
		gearParam = a.GearID.UUID()
	}

	var deletedAtParam any
	if a.DeletedAt != nil {
		deletedAtParam = *a.DeletedAt
	}

	const q = `
		INSERT INTO activities (
			id, user_id,
			type, discipline, is_virtual, is_ebike, is_commute, is_race, custom_subtype,
			title, description,
			start_time, end_time, elapsed_duration_s, moving_duration_s, timezone,
			distance_m, elevation_gain_m, elevation_loss_m, min_elevation_m, max_elevation_m,
			avg_speed_mps, max_speed_mps,
			avg_heart_rate_bpm, max_heart_rate_bpm,
			avg_power_w, max_power_w, normalized_power_w,
			avg_cadence, max_cadence,
			avg_temperature_c, min_temperature_c, max_temperature_c,
			calories_kcal, tss, intensity_factor,
			pool_length_m, total_strokes,
			merge_provenance,
			primary_stream_source_id,
			merged_at,
			gear_id, tags, privacy,
			created_at, updated_at, deleted_at
		) VALUES (
			$1, $2,
			$3, $4, $5, $6, $7, $8, $9,
			$10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23,
			$24, $25,
			$26, $27, $28,
			$29, $30,
			$31, $32, $33,
			$34, $35, $36,
			$37, $38,
			$39,
			$40,
			$41,
			$42, $43, $44,
			COALESCE($45, now()), now(), $46
		)
		ON CONFLICT (id) DO UPDATE SET
			type = EXCLUDED.type,
			discipline = EXCLUDED.discipline,
			is_virtual = EXCLUDED.is_virtual,
			is_ebike = EXCLUDED.is_ebike,
			is_commute = EXCLUDED.is_commute,
			is_race = EXCLUDED.is_race,
			custom_subtype = EXCLUDED.custom_subtype,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			start_time = EXCLUDED.start_time,
			end_time = EXCLUDED.end_time,
			elapsed_duration_s = EXCLUDED.elapsed_duration_s,
			moving_duration_s = EXCLUDED.moving_duration_s,
			timezone = EXCLUDED.timezone,
			distance_m = EXCLUDED.distance_m,
			elevation_gain_m = EXCLUDED.elevation_gain_m,
			elevation_loss_m = EXCLUDED.elevation_loss_m,
			min_elevation_m = EXCLUDED.min_elevation_m,
			max_elevation_m = EXCLUDED.max_elevation_m,
			avg_speed_mps = EXCLUDED.avg_speed_mps,
			max_speed_mps = EXCLUDED.max_speed_mps,
			avg_heart_rate_bpm = EXCLUDED.avg_heart_rate_bpm,
			max_heart_rate_bpm = EXCLUDED.max_heart_rate_bpm,
			avg_power_w = EXCLUDED.avg_power_w,
			max_power_w = EXCLUDED.max_power_w,
			normalized_power_w = EXCLUDED.normalized_power_w,
			avg_cadence = EXCLUDED.avg_cadence,
			max_cadence = EXCLUDED.max_cadence,
			avg_temperature_c = EXCLUDED.avg_temperature_c,
			min_temperature_c = EXCLUDED.min_temperature_c,
			max_temperature_c = EXCLUDED.max_temperature_c,
			calories_kcal = EXCLUDED.calories_kcal,
			tss = EXCLUDED.tss,
			intensity_factor = EXCLUDED.intensity_factor,
			pool_length_m = EXCLUDED.pool_length_m,
			total_strokes = EXCLUDED.total_strokes,
			merge_provenance = EXCLUDED.merge_provenance,
			primary_stream_source_id = EXCLUDED.primary_stream_source_id,
			merged_at = EXCLUDED.merged_at,
			gear_id = EXCLUDED.gear_id,
			tags = EXCLUDED.tags,
			privacy = EXCLUDED.privacy,
			-- created_at: never overwritten (preserves original creation time)
			updated_at = now(),
			deleted_at = EXCLUDED.deleted_at
	`

	// activities.tags is NOT NULL. A nil Go slice binds as SQL NULL and
	// violates the constraint — the column DEFAULT '{}' does not apply when
	// the INSERT supplies the column explicitly. Coalesce to an empty array.
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}

	_, err = db.Exec(ctx, q,
		a.ID.UUID(), a.UserID.UUID(),
		string(a.Type), disciplineParam, a.IsVirtual, a.IsEbike, a.IsCommute, a.IsRace, a.CustomSubtype,
		a.Title, a.Description,
		a.StartTime, a.EndTime, int64(a.ElapsedDuration/time.Second), int64(a.MovingDuration/time.Second), a.Timezone,
		a.Summary.DistanceM, a.Summary.ElevationGainM, a.Summary.ElevationLossM, a.Summary.MinElevationM, a.Summary.MaxElevationM,
		a.Summary.AvgSpeedMps, a.Summary.MaxSpeedMps,
		a.Summary.AvgHeartRateBpm, a.Summary.MaxHeartRateBpm,
		a.Summary.AvgPowerW, a.Summary.MaxPowerW, a.Summary.NormalizedPowerW,
		a.Summary.AvgCadence, a.Summary.MaxCadence,
		a.Summary.AvgTemperatureC, a.Summary.MinTemperatureC, a.Summary.MaxTemperatureC,
		a.Summary.CaloriesKcal, a.Summary.TSS, a.Summary.IntensityFactor,
		a.Summary.PoolLengthM, a.Summary.TotalStrokes,
		provenanceJSON,
		primaryStreamParam,
		a.MergedAt,
		gearParam, tags, string(a.Privacy),
		nullableTime(a.CreatedAt), deletedAtParam,
	)
	if err != nil {
		return fmt.Errorf("save activity %s: %w", a.ID, err)
	}
	return nil
}

// SoftDeleteActivity sets activities.deleted_at.
func (r *ActivityRepo) SoftDeleteActivity(ctx context.Context, id domain.ActivityID, at time.Time) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`UPDATE activities SET deleted_at = $1, updated_at = now() WHERE id = $2 AND deleted_at IS NULL`,
		at, id.UUID(),
	)
	if err != nil {
		return fmt.Errorf("soft delete activity %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Either the row does not exist or is already deleted. We treat
		// both as non-errors — soft-delete is idempotent.
		return nil
	}
	return nil
}

// SetSourceRawBlob records the archived raw-file reference on a source.
func (r *ActivityRepo) SetSourceRawBlob(
	ctx context.Context,
	id domain.SourceID,
	blobID, contentType string,
	sizeBytes int64,
) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`UPDATE activity_sources
		    SET raw_blob_id = $2, raw_content_type = $3, raw_size_bytes = $4, updated_at = now()
		  WHERE id = $1`,
		id.UUID(), blobID, contentType, sizeBytes,
	)
	if err != nil {
		return fmt.Errorf("set source raw blob %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("set source raw blob %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// SetStartLocation writes the denormalised start coordinates and place name.
// Kept separate from SaveActivity so a re-merge never overwrites a resolved
// place (SaveActivity's ON CONFLICT does not list these columns). Pass
// place == "" to mark an activity as "geocode attempted, no place" so the
// backfiller stops reconsidering it.
func (r *ActivityRepo) SetStartLocation(
	ctx context.Context,
	id domain.ActivityID,
	lat, lng *float64,
	place string,
) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activities
		    SET start_lat = $2, start_lng = $3, start_place = $4, updated_at = now()
		  WHERE id = $1`,
		id.UUID(), lat, lng, place,
	)
	if err != nil {
		return fmt.Errorf("set start location %s: %w", id, err)
	}
	return nil
}

// SetStartCoords writes ONLY start_lat/start_lng (not start_place), at ingest
// time, so stage-3 geo-dedup can find existing activities by location.
func (r *ActivityRepo) SetStartCoords(ctx context.Context, id domain.ActivityID, lat, lng float64) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activities SET start_lat = $2, start_lng = $3, updated_at = now() WHERE id = $1`,
		id.UUID(), lat, lng,
	)
	if err != nil {
		return fmt.Errorf("set start coords %s: %w", id, err)
	}
	return nil
}

// ListActivitiesMissingStartPlace returns up to `limit` non-deleted activities
// that have a primary stream but no start_place yet (start_place IS NULL),
// newest first. Used by the geocode backfiller's work queue.
func (r *ActivityRepo) ListActivitiesMissingStartPlace(
	ctx context.Context,
	limit int,
) ([]port.StartPlaceCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT id, primary_stream_source_id
		   FROM activities
		  WHERE start_place IS NULL
		    AND primary_stream_source_id IS NOT NULL
		    AND deleted_at IS NULL
		  ORDER BY start_time DESC
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list activities missing start place: %w", err)
	}
	defer rows.Close()

	var out []port.StartPlaceCandidate
	for rows.Next() {
		var id, srcID uuid.UUID
		if err := rows.Scan(&id, &srcID); err != nil {
			return nil, fmt.Errorf("scan start-place candidate: %w", err)
		}
		out = append(out, port.StartPlaceCandidate{
			ActivityID:            domain.ActivityID(id),
			PrimaryStreamSourceID: domain.SourceID(srcID),
		})
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// ActivitySource
// ---------------------------------------------------------------------------

const sourceColumns = `
	id, activity_id, user_id,
	provider, external_account_id, external_id,
	source_worker_name, source_worker_version, source_worker_package,
	raw_blob_id, raw_content_type, raw_size_bytes,
	parsed,
	status, status_reason,
	reimport_status, reimport_status_reason,
	imported_at, last_reimported_at, updated_at
`

// GetSource reads one activity_sources row by ID.
func (r *ActivityRepo) GetSourceParsedRaw(ctx context.Context, id domain.SourceID) ([]byte, error) {
	db := dbtx(ctx, r.pool)
	var raw []byte
	err := db.QueryRow(ctx, `SELECT parsed FROM activity_sources WHERE id = $1`, id.UUID()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get source parsed %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get source parsed %s: %w", id, err)
	}
	return raw, nil
}

func (r *ActivityRepo) GetSource(ctx context.Context, id domain.SourceID) (domain.ActivitySource, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+sourceColumns+` FROM activity_sources WHERE id = $1`,
		id.UUID(),
	)
	s, err := scanSourceRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ActivitySource{}, fmt.Errorf("get source %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.ActivitySource{}, fmt.Errorf("get source %s: %w", id, err)
	}
	return s, nil
}

// ListSourcesForActivity returns every non-detached source on the activity,
// ordered ImportedAt DESC so the merge engine receives them in the
// expected order.
func (r *ActivityRepo) ListSourcesForActivity(ctx context.Context, id domain.ActivityID) ([]domain.ActivitySource, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+sourceColumns+`
		   FROM activity_sources
		  WHERE activity_id = $1
		    AND status != 'detached'
		  ORDER BY imported_at DESC`,
		id.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list sources for activity %s: %w", id, err)
	}
	defer rows.Close()

	var out []domain.ActivitySource
	for rows.Next() {
		s, err := scanSourceRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source rows: %w", err)
	}
	return out, nil
}

// ListAllSourcesForActivity returns every source on the activity INCLUDING
// detached ones, ordered ImportedAt DESC. The user-facing manage view shows
// detached sources for audit; the merge pipeline uses ListSourcesForActivity.
func (r *ActivityRepo) ListAllSourcesForActivity(ctx context.Context, id domain.ActivityID) ([]domain.ActivitySource, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+sourceColumns+`
		   FROM activity_sources
		  WHERE activity_id = $1
		  ORDER BY imported_at DESC`,
		id.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list all sources for activity %s: %w", id, err)
	}
	defer rows.Close()

	var out []domain.ActivitySource
	for rows.Next() {
		s, err := scanSourceRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source rows: %w", err)
	}
	return out, nil
}

// SaveSource upserts an activity_sources row by ID.
func (r *ActivityRepo) SaveSource(ctx context.Context, s domain.ActivitySource) error {
	db := dbtx(ctx, r.pool)

	parsedJSON, err := encodePayload(s.Parsed)
	if err != nil {
		return fmt.Errorf("encode parsed: %w", err)
	}

	var externalAccountParam any
	if s.ExternalAccountID != nil {
		externalAccountParam = s.ExternalAccountID.UUID()
	}

	var lastReimportedParam any
	if s.LastReimportedAt != nil {
		lastReimportedParam = *s.LastReimportedAt
	}

	// Derived match-feature columns (Phase 2): a projection of Parsed (+ start
	// coords from the stream) the matcher blocks/scores on. Recomputed here on
	// every save so they can never drift from the raw payload.
	mf := sourceMatchFeatures(s)

	const q = `
		INSERT INTO activity_sources (
			id, activity_id, user_id,
			provider, external_account_id, external_id,
			source_worker_name, source_worker_version, source_worker_package,
			raw_blob_id, raw_content_type, raw_size_bytes,
			parsed,
			status, status_reason,
			reimport_status, reimport_status_reason,
			imported_at, last_reimported_at, updated_at,
			sport_class, start_utc, distance_m, moving_s, elapsed_s, start_lat, start_lng
		) VALUES (
			$1, $2, $3,
			$4, $5, $6,
			$7, $8, $9,
			$10, $11, $12,
			$13,
			$14, $15,
			$16, $17,
			COALESCE($18, now()), $19, now(),
			$20, $21, $22, $23, $24, $25, $26
		)
		ON CONFLICT (id) DO UPDATE SET
			-- activity_id, user_id, provider, external_account_id, external_id:
			-- identity columns; never overwritten on reimport
			source_worker_name = EXCLUDED.source_worker_name,
			source_worker_version = EXCLUDED.source_worker_version,
			source_worker_package = EXCLUDED.source_worker_package,
			raw_blob_id = EXCLUDED.raw_blob_id,
			raw_content_type = EXCLUDED.raw_content_type,
			raw_size_bytes = EXCLUDED.raw_size_bytes,
			parsed = EXCLUDED.parsed,
			status = EXCLUDED.status,
			status_reason = EXCLUDED.status_reason,
			reimport_status = EXCLUDED.reimport_status,
			reimport_status_reason = EXCLUDED.reimport_status_reason,
			last_reimported_at = EXCLUDED.last_reimported_at,
			updated_at = now(),
			sport_class = EXCLUDED.sport_class,
			start_utc = EXCLUDED.start_utc,
			distance_m = EXCLUDED.distance_m,
			moving_s = EXCLUDED.moving_s,
			elapsed_s = EXCLUDED.elapsed_s,
			start_lat = COALESCE(EXCLUDED.start_lat, activity_sources.start_lat),
			start_lng = COALESCE(EXCLUDED.start_lng, activity_sources.start_lng)
	`

	_, err = db.Exec(ctx, q,
		s.ID.UUID(), s.ActivityID.UUID(), s.UserID.UUID(),
		s.Provider, externalAccountParam, s.ExternalID,
		s.SourceWorkerName, s.SourceWorkerVersion, s.SourceWorkerPackage,
		s.RawBlobID, s.RawContentType, s.RawSizeBytes,
		parsedJSON,
		string(s.Status), s.StatusReason,
		string(s.ReimportStatus), s.ReimportStatusReason,
		nullableTime(s.ImportedAt), lastReimportedParam,
		mf.sportClass, nullableTime(mf.startUTC), mf.distanceM, mf.movingS, mf.elapsedS, mf.startLat, mf.startLng,
	)
	if err != nil {
		return fmt.Errorf("save source %s: %w", s.ID, err)
	}
	return nil
}

// sourceMF is the derived match-feature projection written alongside a source.
type sourceMF struct {
	sportClass string
	startUTC   time.Time
	distanceM  *float64
	movingS    int64
	elapsedS   int64
	startLat   *float64
	startLng   *float64
}

// sourceMatchFeatures derives the blocking/scoring columns from a source's
// parsed payload (+ stream-derived start coords). Keeping this in one place
// guarantees the denormalized columns stay a faithful projection of `parsed`.
func sourceMatchFeatures(s domain.ActivitySource) sourceMF {
	return sourceMF{
		sportClass: string(s.Parsed.Type),
		startUTC:   s.Parsed.StartTime.UTC(),
		distanceM:  s.Parsed.Summary.DistanceM,
		movingS:    int64(s.Parsed.MovingDuration.Seconds()),
		elapsedS:   int64(s.Parsed.ElapsedDuration.Seconds()),
		startLat:   s.StartLat,
		startLng:   s.StartLng,
	}
}

// ListSourceRecordsInBucket returns the lightweight match-feature view of every
// non-detached source record for a user with start_utc in [from, to). The
// candidate-generation query the re-clustering engine (Phase 3/4) walks per
// (user, UTC-day±margin); sport is NOT filtered (the matcher gates on it so
// synonyms/wildcards still compare). Ordered by source id for deterministic
// clustering tie-breaks.
func (r *ActivityRepo) ListSourceRecordsInBucket(ctx context.Context, userID domain.UserID, from, to time.Time) ([]domain.SourceMatchRecord, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx, `
		SELECT id, activity_id, user_id, provider, external_account_id, external_id,
		       sport_class, start_utc, distance_m, moving_s, elapsed_s,
		       start_lat, start_lng, status
		  FROM activity_sources
		 WHERE user_id = $1
		   AND start_utc >= $2
		   AND start_utc < $3
		   AND status != 'detached'
		 ORDER BY id`,
		userID.UUID(), from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("list source records in bucket: %w", err)
	}
	defer rows.Close()

	var out []domain.SourceMatchRecord
	for rows.Next() {
		var (
			id, activityID, uid uuid.UUID
			extAcct             *uuid.UUID
			provider, extID     string
			sport               string
			startUTC            time.Time
			distanceM           *float64
			movingS, elapsedS   int64
			startLat, startLng  *float64
			status              string
		)
		if err := rows.Scan(&id, &activityID, &uid, &provider, &extAcct, &extID,
			&sport, &startUTC, &distanceM, &movingS, &elapsedS,
			&startLat, &startLng, &status); err != nil {
			return nil, fmt.Errorf("scan match record: %w", err)
		}
		rec := domain.SourceMatchRecord{
			SourceID:   domain.SourceID(id),
			ActivityID: domain.ActivityID(activityID),
			UserID:     domain.UserID(uid),
			Provider:   provider,
			ExternalID: extID,
			SportClass: sport,
			StartUTC:   startUTC,
			DistanceM:  distanceM,
			MovingS:    movingS,
			ElapsedS:   elapsedS,
			StartLat:   startLat,
			StartLng:   startLng,
			Status:     domain.SourceStatus(status),
		}
		if extAcct != nil {
			ea := domain.ExternalAccountID(*extAcct)
			rec.ExternalAccountID = &ea
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate match records: %w", err)
	}
	return out, nil
}

// ReassignSource moves a source to a different logical activity.
func (r *ActivityRepo) ReassignSource(ctx context.Context, sourceID domain.SourceID, newActivityID domain.ActivityID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activity_sources SET activity_id = $1, updated_at = now() WHERE id = $2`,
		newActivityID.UUID(), sourceID.UUID(),
	)
	if err != nil {
		return fmt.Errorf("reassign source %s: %w", sourceID, err)
	}
	return nil
}

// SetActivityMatchState records the matcher confidence band + review flag.
func (r *ActivityRepo) SetActivityMatchState(ctx context.Context, id domain.ActivityID, confidence string, needsReview bool) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activities SET match_confidence = $1, needs_review = $2 WHERE id = $3`,
		confidence, needsReview, id.UUID(),
	)
	if err != nil {
		return fmt.Errorf("set match state %s: %w", id, err)
	}
	return nil
}

// ListActivitiesNeedingReview returns the user's needs_review activities, newest
// first (the confidence-band review queue).
func (r *ActivityRepo) ListActivitiesNeedingReview(ctx context.Context, userID domain.UserID) ([]domain.ActivityReviewItem, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT id, title, type, start_time, match_confidence
		   FROM activities
		  WHERE user_id = $1 AND needs_review AND deleted_at IS NULL
		  ORDER BY start_time DESC`,
		userID.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list activities needing review: %w", err)
	}
	defer rows.Close()
	var out []domain.ActivityReviewItem
	for rows.Next() {
		var (
			id        uuid.UUID
			title, ty string
			start     time.Time
			conf      string
		)
		if err := rows.Scan(&id, &title, &ty, &start, &conf); err != nil {
			return nil, fmt.Errorf("scan review item: %w", err)
		}
		out = append(out, domain.ActivityReviewItem{
			ID:              domain.ActivityID(id),
			Title:           title,
			Type:            domain.ActivityType(ty),
			StartTime:       start,
			MatchConfidence: conf,
		})
	}
	return out, rows.Err()
}

// ClearNeedsReview clears the needs_review flag (user confirmed the merge).
func (r *ActivityRepo) ClearNeedsReview(ctx context.Context, id domain.ActivityID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx, `UPDATE activities SET needs_review = false WHERE id = $1`, id.UUID())
	if err != nil {
		return fmt.Errorf("clear needs_review %s: %w", id, err)
	}
	return nil
}

// AddActivityRedirect records a dissolved-activity → surviving-activity redirect.
func (r *ActivityRepo) AddActivityRedirect(ctx context.Context, oldID, newID domain.ActivityID, reason string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO activity_id_redirects (old_id, new_id, reason)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (old_id) DO UPDATE SET new_id = EXCLUDED.new_id, reason = EXCLUDED.reason`,
		oldID.UUID(), newID.UUID(), reason,
	)
	if err != nil {
		return fmt.Errorf("add activity redirect %s→%s: %w", oldID, newID, err)
	}
	return nil
}

// ListMatchConstraints returns the user's manual matching decisions.
func (r *ActivityRepo) ListMatchConstraints(ctx context.Context, userID domain.UserID) ([]domain.MatchConstraint, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT id, user_id, source_a, source_b, kind, reason, created_at
		   FROM match_constraints WHERE user_id = $1 ORDER BY id`,
		userID.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list match constraints: %w", err)
	}
	defer rows.Close()
	var out []domain.MatchConstraint
	for rows.Next() {
		var (
			id, uid, sa, sb uuid.UUID
			kind, reason    string
			createdAt       time.Time
		)
		if err := rows.Scan(&id, &uid, &sa, &sb, &kind, &reason, &createdAt); err != nil {
			return nil, fmt.Errorf("scan match constraint: %w", err)
		}
		out = append(out, domain.MatchConstraint{
			ID:        domain.MatchConstraintID(id),
			UserID:    domain.UserID(uid),
			SourceA:   domain.SourceID(sa),
			SourceB:   domain.SourceID(sb),
			Kind:      domain.ConstraintKind(kind),
			Reason:    reason,
			CreatedAt: createdAt,
		})
	}
	return out, rows.Err()
}

// AddMatchConstraint records a manual matching decision in canonical pair order.
func (r *ActivityRepo) AddMatchConstraint(ctx context.Context, c domain.MatchConstraint) error {
	db := dbtx(ctx, r.pool)
	a, b := c.SourceA, c.SourceB
	if a.String() > b.String() {
		a, b = b, a
	}
	id := c.ID
	if id == (domain.MatchConstraintID{}) {
		id = domain.MatchConstraintID(uuid.New())
	}
	_, err := db.Exec(ctx,
		`INSERT INTO match_constraints (id, user_id, source_a, source_b, kind, reason)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (source_a, source_b)
		 DO UPDATE SET kind = EXCLUDED.kind, reason = EXCLUDED.reason`,
		id.UUID(), c.UserID.UUID(), a.UUID(), b.UUID(), string(c.Kind), c.Reason,
	)
	if err != nil {
		return fmt.Errorf("add match constraint: %w", err)
	}
	return nil
}

// DetachSource marks the source as detached. The caller should follow up
// with RecomputeActivityFromSources on the parent activity.
func (r *ActivityRepo) DetachSource(ctx context.Context, id domain.SourceID, reason string, at time.Time) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activity_sources
		    SET status = 'detached',
		        status_reason = $1,
		        updated_at = $2
		  WHERE id = $3`,
		reason, at, id.UUID(),
	)
	if err != nil {
		return fmt.Errorf("detach source %s: %w", id, err)
	}
	return nil
}

// SetSourceReimportStatus updates only the reimport_status (+reason) of a
// source — used by RequestSourceReimport to flip a source to 'updating' before
// a re-fetch/re-parse job is published; the ingest pipeline flips it back to
// 'current' when the result lands.
func (r *ActivityRepo) SetSourceReimportStatus(ctx context.Context, id domain.SourceID, status domain.ReimportStatus, reason string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE activity_sources
		    SET reimport_status = $1, reimport_status_reason = $2, updated_at = now()
		  WHERE id = $3`,
		string(status), reason, id.UUID(),
	)
	if err != nil {
		return fmt.Errorf("set reimport status for source %s: %w", id, err)
	}
	return nil
}

// MarkSourcesOutOfDate flags every 'current' source as update_available when a
// newer worker FROM THE SAME PACKAGE for the SAME PROVIDER arrives. The match
// key is (provider, source_worker_package, version < newVersion) — the routing
// name/alias is deliberately NOT part of it. Versions are simple incrementing
// integers (#54); non-numeric stored versions are skipped defensively. Empty
// package never matches (legacy rows imported before package-stamping).
func (r *ActivityRepo) MarkSourcesOutOfDate(
	ctx context.Context,
	provider, pkg string,
	newVersion int,
) (int, error) {
	if provider == "" || pkg == "" {
		return 0, nil
	}
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`UPDATE activity_sources
		    SET reimport_status        = 'update_available',
		        reimport_status_reason = 'newer_worker_version'
		  WHERE provider = $1
		    AND source_worker_package = $2
		    AND reimport_status = 'current'
		    AND source_worker_version ~ '^[0-9]+$'
		    AND source_worker_version::int < $3`,
		provider, pkg, newVersion,
	)
	if err != nil {
		return 0, fmt.Errorf("mark sources out-of-date for %s/%s: %w", provider, pkg, err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Dedup
// ---------------------------------------------------------------------------

// ExistingExternalIDs returns the provider external_ids already imported for
// an external account (any non-detached source).
func (r *ActivityRepo) ExistingExternalIDs(ctx context.Context, provider string, accountID domain.ExternalAccountID) (map[string]struct{}, error) {
	rows, err := dbtx(ctx, r.pool).Query(ctx,
		`SELECT external_id FROM activity_sources
		 WHERE provider = $1 AND external_account_id = $2 AND status <> 'detached'`,
		provider, accountID.UUID())
	if err != nil {
		return nil, fmt.Errorf("existing external ids: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// FindSourceByExternalID implements dedup stage 1.
func (r *ActivityRepo) FindSourceByExternalID(
	ctx context.Context,
	provider string,
	externalAccountID *domain.ExternalAccountID,
	externalID string,
) (domain.ActivitySource, error) {
	db := dbtx(ctx, r.pool)

	var accountParam any
	if externalAccountID != nil {
		accountParam = externalAccountID.UUID()
	}

	// External_account_id may be NULL for manual uploads — the UNIQUE
	// constraint in migration 5 treats NULL as distinct in Postgres, but
	// dedup must treat (provider, NULL, external_id) as a single bucket
	// per user. We disambiguate via IS NOT DISTINCT FROM.
	row := db.QueryRow(ctx,
		`SELECT `+sourceColumns+`
		   FROM activity_sources
		  WHERE provider = $1
		    AND external_account_id IS NOT DISTINCT FROM $2
		    AND external_id = $3
		    AND status != 'detached'
		  LIMIT 1`,
		provider, accountParam, externalID,
	)
	s, err := scanSourceRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ActivitySource{}, fmt.Errorf("find source by external id: %w", domain.ErrNotFound)
	}
	if err != nil {
		return domain.ActivitySource{}, fmt.Errorf("find source by external id: %w", err)
	}
	return s, nil
}

// ListActivitiesForUser returns activities for the user within [start, end]
// ordered by start_time ASC. Soft-deleted rows are excluded.
func (r *ActivityRepo) ListActivitiesForUser(
	ctx context.Context,
	userID domain.UserID,
	start, end time.Time,
) ([]domain.Activity, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+`
		   FROM activities
		  WHERE user_id = $1
		    AND start_time BETWEEN $2 AND $3
		    AND deleted_at IS NULL
		  ORDER BY start_time ASC`,
		userID.UUID(), start, end,
	)
	if err != nil {
		return nil, fmt.Errorf("list activities for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan activity row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user activities: %w", err)
	}
	return out, nil
}

// ListRecentActivitiesForUser returns the user's newest `limit` activities
// ordered by start_time DESC, regardless of age (count-based pagination for
// the feed). Soft-deleted rows excluded.
func (r *ActivityRepo) ListRecentActivitiesForUser(
	ctx context.Context,
	userID domain.UserID,
	limit int,
) ([]domain.Activity, error) {
	if limit <= 0 {
		limit = 50
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+`
		   FROM activities
		  WHERE user_id = $1
		    AND deleted_at IS NULL
		  ORDER BY start_time DESC
		  LIMIT $2`,
		userID.UUID(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recent activities for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan activity row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent activities: %w", err)
	}
	return out, nil
}

// ListPublicActivitiesForUser returns a user's public, non-hidden activities
// newest-first — the source for their ActivityPub outbox (federation Phase 3).
func (r *ActivityRepo) ListPublicActivitiesForUser(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.Activity, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+`
		   FROM activities
		  WHERE user_id = $1 AND deleted_at IS NULL
		    AND privacy = 'public' AND hidden_by_admin = false
		  ORDER BY start_time DESC
		  LIMIT $2 OFFSET $3`,
		userID.UUID(), limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list public activities for %s: %w", userID, err)
	}
	defer rows.Close()
	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan activity row: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPublicActivitiesForUser counts a user's public, non-hidden activities
// (the outbox totalItems).
func (r *ActivityRepo) CountPublicActivitiesForUser(ctx context.Context, userID domain.UserID) (int, error) {
	db := dbtx(ctx, r.pool)
	var n int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM activities
		  WHERE user_id = $1 AND deleted_at IS NULL
		    AND privacy = 'public' AND hidden_by_admin = false`,
		userID.UUID()).Scan(&n); err != nil {
		return 0, fmt.Errorf("count public activities for %s: %w", userID, err)
	}
	return n, nil
}

// ActivityTotals returns aggregate sums across the user's non-deleted activities.
func (r *ActivityRepo) ActivityTotals(ctx context.Context, userID domain.UserID) (port.ActivityTotalsResult, error) {
	db := dbtx(ctx, r.pool)
	var out port.ActivityTotalsResult
	err := db.QueryRow(ctx,
		`SELECT count(*),
		        COALESCE(sum(distance_m), 0),
		        COALESCE(sum(moving_duration_s), 0),
		        COALESCE(sum(elevation_gain_m), 0)
		   FROM activities
		  WHERE user_id = $1 AND deleted_at IS NULL`,
		userID.UUID()).Scan(&out.Count, &out.DistanceM, &out.MovingS, &out.ElevationGainM)
	if err != nil {
		return port.ActivityTotalsResult{}, fmt.Errorf("activity totals: %w", err)
	}
	return out, nil
}

// ActivityYearStats returns per-calendar-year totals (newest first).
func (r *ActivityRepo) ActivityYearStats(ctx context.Context, userID domain.UserID) ([]port.ActivityYearStat, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT EXTRACT(YEAR FROM start_time)::int AS yr,
		        count(*),
		        COALESCE(sum(distance_m), 0),
		        COALESCE(sum(moving_duration_s), 0),
		        COALESCE(sum(elevation_gain_m), 0)
		   FROM activities
		  WHERE user_id = $1 AND deleted_at IS NULL
		  GROUP BY yr ORDER BY yr DESC`,
		userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("activity year stats: %w", err)
	}
	defer rows.Close()
	var out []port.ActivityYearStat
	for rows.Next() {
		var s port.ActivityYearStat
		if err := rows.Scan(&s.Year, &s.Count, &s.DistanceM, &s.MovingS, &s.ElevationGainM); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// activityFilterConds translates an ActivityListFilter into WHERE conditions
// and their args, starting from the user + not-deleted base. excludeDim names
// a facet dimension ("type" | "discipline") whose own equality filter is
// skipped — a facet must ignore its own dimension so the user can still switch
// within it, while respecting every other active filter.
func activityFilterConds(
	userID domain.UserID,
	filter port.ActivityListFilter,
	excludeDim string,
) (conds []string, args []any) {
	conds = []string{"user_id = $1", "deleted_at IS NULL"}
	args = []any{userID.UUID()}

	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}

	if filter.Type != "" && excludeDim != "type" {
		add("type = $%d", filter.Type)
	}
	if filter.Discipline != "" && excludeDim != "discipline" {
		add("discipline = $%d", filter.Discipline)
	}
	if !filter.From.IsZero() {
		add("start_time >= $%d", filter.From)
	}
	if !filter.To.IsZero() {
		add("start_time < $%d", filter.To)
	}

	for _, f := range []struct {
		col string
		val *bool
	}{
		{"is_virtual", filter.IsVirtual},
		{"is_ebike", filter.IsEbike},
		{"is_commute", filter.IsCommute},
		{"is_race", filter.IsRace},
	} {
		if f.val != nil {
			add(f.col+" = $%d", *f.val)
		}
	}

	// Bounded ranges: a NULL column value never matches (SQL comparison with
	// NULL is not true), which is the intended "unknown doesn't match" rule.
	for _, rng := range []struct {
		col      string
		min, max *float64
	}{
		{"distance_m", filter.DistanceMinM, filter.DistanceMaxM},
		{"elapsed_duration_s", filter.DurationMinS, filter.DurationMaxS},
		{"elevation_gain_m", filter.ElevationMinM, filter.ElevationMaxM},
		{"avg_speed_mps", filter.AvgSpeedMinMps, filter.AvgSpeedMaxMps},
		{"avg_heart_rate_bpm", filter.AvgHRMinBpm, filter.AvgHRMaxBpm},
		{"avg_power_w", filter.AvgPowerMinW, filter.AvgPowerMaxW},
	} {
		if rng.min != nil {
			add(rng.col+" >= $%d", *rng.min)
		}
		if rng.max != nil {
			add(rng.col+" <= $%d", *rng.max)
		}
	}

	return conds, args
}

// ActivityFacets returns per-type and per-discipline counts (only values the
// user has) plus the grand total. Empty discipline values are skipped in the
// discipline facet (they mean "no sub-classification").
func (r *ActivityRepo) ActivityFacets(
	ctx context.Context,
	userID domain.UserID,
	filter port.ActivityListFilter,
) (types, disciplines []port.ActivityFacet, total int, err error) {
	db := dbtx(ctx, r.pool)

	// facet counts distinct values of `col`, respecting every active filter
	// except the facet's own dimension.
	facet := func(col string, skipEmpty bool) ([]port.ActivityFacet, error) {
		conds, args := activityFilterConds(userID, filter, col)
		if skipEmpty {
			conds = append(conds, col+" <> ''")
		}
		rows, qerr := db.Query(ctx,
			`SELECT `+col+`, count(*) FROM activities WHERE `+strings.Join(conds, " AND ")+
				` GROUP BY `+col+` ORDER BY count(*) DESC`, args...)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		var out []port.ActivityFacet
		for rows.Next() {
			var f port.ActivityFacet
			if e := rows.Scan(&f.Value, &f.Count); e != nil {
				return nil, e
			}
			out = append(out, f)
		}
		return out, rows.Err()
	}

	if types, err = facet("type", false); err != nil {
		return nil, nil, 0, fmt.Errorf("facet type: %w", err)
	}
	if disciplines, err = facet("discipline", true); err != nil {
		return nil, nil, 0, fmt.Errorf("facet discipline: %w", err)
	}
	if err = db.QueryRow(ctx,
		`SELECT count(*) FROM activities WHERE user_id = $1 AND deleted_at IS NULL`,
		userID.UUID()).Scan(&total); err != nil {
		return nil, nil, 0, fmt.Errorf("facet total: %w", err)
	}
	return types, disciplines, total, nil
}

// activitySortSQL maps the filter sort key to an ORDER BY clause. Unknown keys
// fall back to newest-first.
func activitySortSQL(sort string) string {
	switch sort {
	case "date_asc":
		return "start_time ASC"
	case "distance_desc":
		return "distance_m DESC NULLS LAST"
	case "duration_desc":
		return "elapsed_duration_s DESC NULLS LAST"
	case "elevation_desc":
		return "elevation_gain_m DESC NULLS LAST"
	case "speed_desc":
		return "avg_speed_mps DESC NULLS LAST"
	default: // "date_desc"
		return "start_time DESC"
	}
}

// ListActivitiesFiltered returns a filtered+sorted page plus the total count
// matching the filter (ignoring limit).
func (r *ActivityRepo) ListActivitiesFiltered(
	ctx context.Context,
	userID domain.UserID,
	filter port.ActivityListFilter,
) ([]domain.Activity, int, error) {
	db := dbtx(ctx, r.pool)

	conds, args := activityFilterConds(userID, filter, "")
	where := strings.Join(conds, " AND ")

	var matched int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM activities WHERE `+where, args...).Scan(&matched); err != nil {
		return nil, 0, fmt.Errorf("count filtered activities: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit)
	limitIdx := len(args)
	args = append(args, offset)
	offsetIdx := len(args)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+`
		   FROM activities WHERE `+where+
			// id tiebreak keeps ordering stable across pages when sort keys tie.
			` ORDER BY `+activitySortSQL(filter.Sort)+`, id DESC`+
			fmt.Sprintf(` LIMIT $%d OFFSET $%d`, limitIdx, offsetIdx),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list filtered activities: %w", err)
	}
	defer rows.Close()

	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan activity row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate filtered activities: %w", err)
	}
	return out, matched, nil
}

// ListFollowingFeed returns non-private activities owned by users the viewer
// follows (accepted edges), newest first. The privacy filter is coarse here
// (private excluded); per-category projection happens at the read path.
func (r *ActivityRepo) ListFollowingFeed(
	ctx context.Context,
	viewerID domain.UserID,
	limit, offset int,
) ([]domain.Activity, error) {
	if limit <= 0 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+`
		   FROM activities a
		   WHERE a.deleted_at IS NULL
		     AND a.privacy <> 'private'
		     AND a.hidden_by_admin = false
		     AND a.user_id IN (
		         SELECT followee_id FROM follows
		         WHERE follower_id = $1 AND status = 'accepted'
		     )
		   ORDER BY a.start_time DESC, a.id DESC
		   LIMIT $2 OFFSET $3`,
		viewerID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list following feed: %w", err)
	}
	defer rows.Close()
	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan feed row: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// scan helpers
// ---------------------------------------------------------------------------

// rowScanner is the common scan surface of pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanActivityRow(row rowScanner) (domain.Activity, error) {
	var (
		id, userID                              uuid.UUID
		typ                                     string
		discipline                              *string
		isVirtual, isEbike, isCommute, isRace   bool
		customSubtype, title, description       string
		startTime, endTime                      time.Time
		elapsedS, movingS                       int64
		timezone                                string
		distanceM, elevationGainM               *float64
		elevationLossM, minElevationM, maxElevM *float64
		avgSpeed, maxSpeed                      *float64
		avgHR, maxHR                            *int
		avgPower, maxPower, normPower           *int
		avgCadence, maxCadence                  *int
		avgTemp, minTemp, maxTemp               *float64
		calories                                *int
		tss, intensityFactor                    *float64
		poolLength, totalStrokes                *int
		provenanceRaw                           []byte
		primaryStreamSourceID                   *uuid.UUID
		mergedAt                                time.Time
		gearID                                  *uuid.UUID
		tags                                    []string
		privacy                                 string
		createdAt, updatedAt                    time.Time
		deletedAt                               *time.Time
		startLat, startLng                      *float64
		startPlace                              *string
		hiddenByAdmin                           bool
	)

	err := row.Scan(
		&id, &userID,
		&typ, &discipline, &isVirtual, &isEbike, &isCommute, &isRace, &customSubtype,
		&title, &description,
		&startTime, &endTime, &elapsedS, &movingS, &timezone,
		&distanceM, &elevationGainM, &elevationLossM, &minElevationM, &maxElevM,
		&avgSpeed, &maxSpeed,
		&avgHR, &maxHR,
		&avgPower, &maxPower, &normPower,
		&avgCadence, &maxCadence,
		&avgTemp, &minTemp, &maxTemp,
		&calories, &tss, &intensityFactor,
		&poolLength, &totalStrokes,
		&provenanceRaw,
		&primaryStreamSourceID,
		&mergedAt,
		&gearID, &tags, &privacy,
		&createdAt, &updatedAt, &deletedAt,
		&startLat, &startLng, &startPlace,
		&hiddenByAdmin,
	)
	if err != nil {
		return domain.Activity{}, err
	}

	prov, err := decodeProvenance(provenanceRaw)
	if err != nil {
		return domain.Activity{}, err
	}

	a := domain.Activity{
		ID:              domain.ActivityID(id),
		UserID:          domain.UserID(userID),
		Type:            domain.ActivityType(typ),
		IsVirtual:       isVirtual,
		IsEbike:         isEbike,
		IsCommute:       isCommute,
		IsRace:          isRace,
		CustomSubtype:   customSubtype,
		Title:           title,
		Description:     description,
		StartTime:       startTime,
		EndTime:         endTime,
		ElapsedDuration: time.Duration(elapsedS) * time.Second,
		MovingDuration:  time.Duration(movingS) * time.Second,
		Timezone:        timezone,
		Summary: domain.ActivitySummary{
			DistanceM:        distanceM,
			ElevationGainM:   elevationGainM,
			ElevationLossM:   elevationLossM,
			MinElevationM:    minElevationM,
			MaxElevationM:    maxElevM,
			AvgSpeedMps:      avgSpeed,
			MaxSpeedMps:      maxSpeed,
			AvgHeartRateBpm:  avgHR,
			MaxHeartRateBpm:  maxHR,
			AvgPowerW:        avgPower,
			MaxPowerW:        maxPower,
			NormalizedPowerW: normPower,
			AvgCadence:       avgCadence,
			MaxCadence:       maxCadence,
			AvgTemperatureC:  avgTemp,
			MinTemperatureC:  minTemp,
			MaxTemperatureC:  maxTemp,
			CaloriesKcal:     calories,
			TSS:              tss,
			IntensityFactor:  intensityFactor,
			PoolLengthM:      poolLength,
			TotalStrokes:     totalStrokes,
		},
		MergeProvenance: prov,
		MergedAt:        mergedAt,
		Tags:            tags,
		Privacy:         domain.ActivityPrivacy(privacy),
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		DeletedAt:       deletedAt,
	}
	a.StartLat = startLat
	a.StartLng = startLng
	a.HiddenByAdmin = hiddenByAdmin
	if startPlace != nil {
		a.StartPlace = *startPlace
	}
	if discipline != nil {
		a.Discipline = domain.Discipline(*discipline)
	}
	if primaryStreamSourceID != nil {
		sid := domain.SourceID(*primaryStreamSourceID)
		a.PrimaryStreamSourceID = &sid
	}
	if gearID != nil {
		gid := domain.GearID(*gearID)
		a.GearID = &gid
	}
	return a, nil
}

func scanSourceRow(row rowScanner) (domain.ActivitySource, error) {
	var (
		id, activityID, userID uuid.UUID
		provider               string
		externalAccountID      *uuid.UUID
		externalID             string
		workerName, workerVer  string
		workerPackage          string
		rawBlobID, rawContent  string
		rawSize                int64
		parsedRaw              []byte
		status, statusReason   string
		reimport, reimportReas string
		importedAt             time.Time
		lastReimportedAt       *time.Time
		updatedAt              time.Time
	)

	err := row.Scan(
		&id, &activityID, &userID,
		&provider, &externalAccountID, &externalID,
		&workerName, &workerVer, &workerPackage,
		&rawBlobID, &rawContent, &rawSize,
		&parsedRaw,
		&status, &statusReason,
		&reimport, &reimportReas,
		&importedAt, &lastReimportedAt, &updatedAt,
	)
	if err != nil {
		return domain.ActivitySource{}, err
	}

	parsed, err := decodePayload(parsedRaw)
	if err != nil {
		return domain.ActivitySource{}, err
	}

	s := domain.ActivitySource{
		ID:                   domain.SourceID(id),
		ActivityID:           domain.ActivityID(activityID),
		UserID:               domain.UserID(userID),
		Provider:             provider,
		ExternalID:           externalID,
		SourceWorkerName:     workerName,
		SourceWorkerVersion:  workerVer,
		SourceWorkerPackage:  workerPackage,
		RawBlobID:            rawBlobID,
		RawContentType:       rawContent,
		RawSizeBytes:         rawSize,
		Parsed:               parsed,
		Status:               domain.SourceStatus(status),
		StatusReason:         statusReason,
		ReimportStatus:       domain.ReimportStatus(reimport),
		ReimportStatusReason: reimportReas,
		ImportedAt:           importedAt,
		LastReimportedAt:     lastReimportedAt,
		UpdatedAt:            updatedAt,
	}
	if externalAccountID != nil {
		eaID := domain.ExternalAccountID(*externalAccountID)
		s.ExternalAccountID = &eaID
	}
	return s, nil
}

// nullableTime returns the time when non-zero, else nil. Used for
// COALESCE-driven default-to-now() semantics on insert.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// ActivityTotalsForRange returns totals for activities whose start_time falls in
// [start, end). Used for "this week" / "last week" dashboard stats.
func (r *ActivityRepo) ActivityTotalsForRange(ctx context.Context, userID domain.UserID, start, end time.Time) (port.ActivityTotalsResult, error) {
	db := dbtx(ctx, r.pool)
	var out port.ActivityTotalsResult
	err := db.QueryRow(ctx,
		`SELECT count(*),
		        COALESCE(sum(distance_m), 0),
		        COALESCE(sum(moving_duration_s), 0),
		        COALESCE(sum(elevation_gain_m), 0)
		   FROM activities
		  WHERE user_id = $1 AND deleted_at IS NULL
		    AND start_time >= $2 AND start_time < $3`,
		userID.UUID(), start.UTC(), end.UTC()).Scan(&out.Count, &out.DistanceM, &out.MovingS, &out.ElevationGainM)
	if err != nil {
		return port.ActivityTotalsResult{}, fmt.Errorf("activity totals for range: %w", err)
	}
	return out, nil
}
