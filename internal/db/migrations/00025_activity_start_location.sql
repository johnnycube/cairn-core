-- +goose Up

-- Denormalised start location for the activity feed/detail subtitle
-- ("Ride from Darmstadt"). start_lat/start_lng cache the first GPS point of
-- the primary stream so re-geocoding never has to re-read the stream;
-- start_place holds the reverse-geocoded place name.
--
-- start_place semantics (drives the backfiller's work queue):
--   NULL  → not yet attempted (the geocode backfiller picks these up)
--   ''    → attempted, but no usable place (no GPS, or geocoder returned none)
--   text  → resolved place name
--
-- These columns are intentionally NOT written by SaveActivity/recompute — a
-- re-merge must not clobber a resolved place. They are set only by the
-- dedicated SetStartLocation UPDATE, so ON CONFLICT DO UPDATE leaves them
-- untouched.
ALTER TABLE activities
    ADD COLUMN start_lat   double precision,
    ADD COLUMN start_lng   double precision,
    ADD COLUMN start_place text;

-- Partial index for the backfiller's "next batch of un-geocoded activities
-- that have a stream" query.
CREATE INDEX activities_start_place_pending_idx
    ON activities (start_time DESC)
    WHERE start_place IS NULL
      AND primary_stream_source_id IS NOT NULL
      AND deleted_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS activities_start_place_pending_idx;
ALTER TABLE activities
    DROP COLUMN IF EXISTS start_lat,
    DROP COLUMN IF EXISTS start_lng,
    DROP COLUMN IF EXISTS start_place;
