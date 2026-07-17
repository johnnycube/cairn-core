-- 00015_provider_agnostic_segments.sql
--
-- Removes Strava-specific hardcoding from the segments table. Renames
-- the `source` enum values:
--
--   'strava' → 'external'   (external segment of any provider; provider
--                            identity is implicit via external_account_id
--                            → external_accounts.provider)
--   'cairn'  → 'native'     (Cairn-native segment, owned by a user)
--
-- This decouples the domain from any specific provider. Future providers
-- (Garmin segments, Polar climbs, etc.) reuse the 'external' branch
-- without any further domain changes. If a provider later requires a
-- different validation rule (e.g. publicly-shareable external segments),
-- we add an `external_provider` text column then; for v1 the existing
-- "external segments must be PRIVATE scope" rule applies generically.
--
-- Backfill is done in-place; the CHECK constraint is dropped/recreated
-- around the UPDATE so the intermediate state (rows with old values)
-- doesn't violate the new constraint.

-- +goose Up
ALTER TABLE segments DROP CONSTRAINT segments_source_check;

UPDATE segments SET source = 'external' WHERE source = 'strava';
UPDATE segments SET source = 'native'   WHERE source = 'cairn';

ALTER TABLE segments
    ADD CONSTRAINT segments_source_check
    CHECK (source IN ('external', 'native'));

-- The two scope-validation triggers (defined in migration 7) reference
-- the source values in their CHECK bodies; rewrite them so they keep
-- enforcing the same logical rules after the rename.
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_external_requires_external_id;
ALTER TABLE segments
    ADD CONSTRAINT segments_external_requires_external_id
    CHECK (
        source != 'external'
        OR (external_id IS NOT NULL AND external_account_id IS NOT NULL)
    );

ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_external_no_owner;
ALTER TABLE segments
    ADD CONSTRAINT segments_external_no_owner
    CHECK (source != 'external' OR owner_user_id IS NULL);

ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_native_requires_owner;
ALTER TABLE segments
    ADD CONSTRAINT segments_native_requires_owner
    CHECK (
        source != 'native'
        OR owner_user_id IS NOT NULL
    );

ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_native_no_external;
ALTER TABLE segments
    ADD CONSTRAINT segments_native_no_external
    CHECK (
        source != 'native'
        OR (external_id IS NULL AND external_account_id IS NULL)
    );

-- +goose Down
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_external_requires_external_id;
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_external_no_owner;
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_native_requires_owner;
ALTER TABLE segments DROP CONSTRAINT IF EXISTS segments_native_no_external;

ALTER TABLE segments DROP CONSTRAINT segments_source_check;

UPDATE segments SET source = 'strava' WHERE source = 'external';
UPDATE segments SET source = 'cairn'  WHERE source = 'native';

ALTER TABLE segments
    ADD CONSTRAINT segments_source_check
    CHECK (source IN ('strava', 'cairn'));
