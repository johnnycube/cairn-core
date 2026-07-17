-- Denormalized match features (a projection of activity_sources.parsed) so the
-- matcher can bucket candidates without parsing jsonb. Repopulated on every
-- save. All times UTC.

-- +goose Up
ALTER TABLE activity_sources
    ADD COLUMN sport_class text NOT NULL DEFAULT '',
    ADD COLUMN start_utc    timestamptz,
    ADD COLUMN distance_m   double precision,
    ADD COLUMN moving_s     bigint,
    ADD COLUMN elapsed_s    bigint,
    ADD COLUMN start_lat    double precision,
    ADD COLUMN start_lng    double precision;

-- Backfill from the parsed payload. Field names match payload_json.go.
UPDATE activity_sources SET
    sport_class = COALESCE(parsed ->> 'type', ''),
    start_utc   = NULLIF(parsed ->> 'start_time', '')::timestamptz,
    distance_m  = (parsed -> 'summary' ->> 'distance_m')::double precision,
    moving_s    = NULLIF(parsed ->> 'moving_duration_s', '')::bigint,
    elapsed_s   = NULLIF(parsed ->> 'elapsed_duration_s', '')::bigint;

-- Seed per-source start coordinates from the owning activity when the activity
-- already has a resolved start point (migration 25). Source-level coords are
-- otherwise written at ingest from the stream's first GPS sample.
UPDATE activity_sources s SET
    start_lat = a.start_lat,
    start_lng = a.start_lng
FROM activities a
WHERE a.id = s.activity_id
  AND a.start_lat IS NOT NULL
  AND a.start_lng IS NOT NULL;

-- Blocking index: candidate generation buckets by user + sport + UTC time.
CREATE INDEX activity_sources_match_bucket_idx
    ON activity_sources (user_id, sport_class, start_utc);

-- +goose Down
DROP INDEX IF EXISTS activity_sources_match_bucket_idx;
ALTER TABLE activity_sources
    DROP COLUMN sport_class,
    DROP COLUMN start_utc,
    DROP COLUMN distance_m,
    DROP COLUMN moving_s,
    DROP COLUMN elapsed_s,
    DROP COLUMN start_lat,
    DROP COLUMN start_lng;
