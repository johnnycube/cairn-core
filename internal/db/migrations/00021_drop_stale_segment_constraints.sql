-- +goose Up

-- Migration 16 renamed segments.source values strava→external and cairn→native
-- and added shape constraints keyed on the NEW values
-- (segments_external_requires_external_id, _external_no_owner,
-- _native_requires_owner, _native_no_external). But it left two OLD constraints
-- that still reference 'strava'/'cairn', so any insert with the new 'external'
-- value fails. The table was empty, so this lay dormant until external segment
-- ingestion exercised it.
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_source_shape_chk;

-- segments_strava_scope_chk enforced "strava segments are private" against the
-- old value (vacuously true for 'external'). Replace it with the equivalent on
-- the new value so external segments are still constrained to private scope.
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_strava_scope_chk;
ALTER TABLE segments
    ADD CONSTRAINT segments_external_scope_chk
    CHECK (source <> 'external' OR scope = 'private');

-- +goose Down
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_external_scope_chk;
