-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- gear
--
-- Optional bike/shoe records the user can attach to activities. Tracked
-- separately for total-distance reporting.
-- ----------------------------------------------------------------------------

CREATE TABLE gear (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            text NOT NULL
                    CHECK (kind IN ('bike', 'shoe', 'other')),
    name            text NOT NULL,
    brand           text,
    model           text,
    purchase_date   date,
    retired         boolean NOT NULL DEFAULT false,
    notes           text,
    -- Maintained by a trigger / job; denormalized for fast list rendering.
    total_distance_m double precision NOT NULL DEFAULT 0,
    total_duration_s bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX gear_user_id_idx ON gear (user_id) WHERE retired = false;

CREATE TRIGGER gear_set_updated_at
    BEFORE UPDATE ON gear
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- activities
--
-- The merged domain aggregate. One row per workout. Sources are kept
-- separately in activity_sources; the columns here are the post-merge
-- view computed by the use case `RecomputeActivityFromSources`.
--
-- Numeric columns use SI units throughout. Optional values are NULL when
-- no source reported them; this is distinct from a zero value.
-- ----------------------------------------------------------------------------

CREATE TABLE activities (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Classification
    type                        text NOT NULL
                                CHECK (type IN ('ride','run','swim','hike','walk','row','ski','workout')),
    discipline                  text
                                CHECK (discipline IS NULL OR discipline IN (
                                    -- ride
                                    'ride_road','ride_mtb','ride_gravel','ride_cyclocross','ride_track','ride_bmx',
                                    -- run
                                    'run_road','run_trail','run_track',
                                    -- swim
                                    'swim_pool','swim_open_water',
                                    -- ski
                                    'ski_alpine','ski_nordic','ski_touring','ski_backcountry'
                                )),
    is_virtual                  boolean NOT NULL DEFAULT false,
    is_ebike                    boolean NOT NULL DEFAULT false,
    is_commute                  boolean NOT NULL DEFAULT false,
    is_race                     boolean NOT NULL DEFAULT false,
    custom_subtype              text NOT NULL DEFAULT '',

    title                       text NOT NULL DEFAULT '',
    description                 text NOT NULL DEFAULT '',

    -- Time
    start_time                  timestamptz NOT NULL,
    end_time                    timestamptz NOT NULL,
    elapsed_duration_s          bigint NOT NULL,
    moving_duration_s           bigint NOT NULL,
    timezone                    text NOT NULL DEFAULT 'UTC',

    -- Summary metrics (denormalized from the winning source per field-group).
    distance_m                  double precision,
    elevation_gain_m            double precision,
    elevation_loss_m            double precision,
    min_elevation_m             double precision,
    max_elevation_m             double precision,
    avg_speed_mps               double precision,
    max_speed_mps               double precision,
    avg_heart_rate_bpm          integer,
    max_heart_rate_bpm          integer,
    avg_power_w                 integer,
    max_power_w                 integer,
    normalized_power_w          integer,
    avg_cadence                 integer,
    max_cadence                 integer,
    avg_temperature_c           double precision,
    min_temperature_c           double precision,
    max_temperature_c           double precision,
    calories_kcal               integer,
    tss                         double precision,
    intensity_factor            double precision,
    pool_length_m               integer,
    total_strokes               integer,

    -- Source-pick provenance: which source won each field group.
    -- Keys: distance, duration, elevation, heart_rate, power, cadence,
    -- temperature, speed, gps_track, calories.
    merge_provenance            jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- The primary stream-bearing source — what charts and maps render by default.
    primary_stream_source_id    uuid,                       -- FK declared below after activity_sources

    merged_at                   timestamptz NOT NULL DEFAULT now(),

    gear_id                     uuid REFERENCES gear(id) ON DELETE SET NULL,
    tags                        text[] NOT NULL DEFAULT '{}'::text[],
    privacy                     text NOT NULL DEFAULT 'private'
                                CHECK (privacy IN ('private', 'followers', 'public')),

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,

    CONSTRAINT activities_time_order_chk CHECK (end_time >= start_time),
    CONSTRAINT activities_duration_chk CHECK (elapsed_duration_s >= 0 AND moving_duration_s >= 0)
);

-- The activity feed is by far the hottest query path; this is the index it uses.
CREATE INDEX activities_user_start_time_idx
    ON activities (user_id, start_time DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX activities_user_type_start_time_idx
    ON activities (user_id, type, start_time DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX activities_user_discipline_start_time_idx
    ON activities (user_id, discipline, start_time DESC)
    WHERE deleted_at IS NULL AND discipline IS NOT NULL;

-- Tag search via GIN.
CREATE INDEX activities_tags_idx ON activities USING gin (tags)
    WHERE deleted_at IS NULL;

-- Deleted retention queries.
CREATE INDEX activities_deleted_at_idx ON activities (deleted_at)
    WHERE deleted_at IS NOT NULL;

CREATE TRIGGER activities_set_updated_at
    BEFORE UPDATE ON activities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- activity_sources
--
-- One row per (activity, provider, external_account, external_id) tuple.
-- Many sources can attach to one activity (Strava + Garmin for the same ride).
-- ----------------------------------------------------------------------------

CREATE TABLE activity_sources (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id                 uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    -- Denormalized for cheaper filters (e.g. "all Garmin imports last month").
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    provider                    text NOT NULL,
    external_account_id         uuid REFERENCES external_accounts(id) ON DELETE SET NULL,
    external_id                 text NOT NULL,

    source_worker_name          text NOT NULL,
    source_worker_version       text NOT NULL,
    source_manifest_hash        text NOT NULL,

    raw_blob_id                 text NOT NULL DEFAULT '',          -- S3 object key (empty = no raw blob stored)
    raw_content_type            text NOT NULL DEFAULT '',
    raw_size_bytes              bigint NOT NULL DEFAULT 0,

    -- Full-fidelity parsed payload (cairn.worker.v1.ActivitySourcePayload).
    -- Used by the merge engine and by source-comparison UI.
    parsed                      jsonb NOT NULL,

    status                      text NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','orphaned','account_unavailable','detached')),
    status_reason               text NOT NULL DEFAULT '',

    reimport_status             text NOT NULL DEFAULT 'current'
                                CHECK (reimport_status IN ('current','update_available','updating','failed')),
    reimport_status_reason      text NOT NULL DEFAULT '',

    imported_at                 timestamptz NOT NULL DEFAULT now(),
    last_reimported_at          timestamptz,
    updated_at                  timestamptz NOT NULL DEFAULT now(),

    -- Exact-match dedup: a given external_id from a given account from a
    -- given provider maps to a single source row, ever.
    UNIQUE (provider, external_account_id, external_id)
);

CREATE INDEX activity_sources_activity_id_idx ON activity_sources (activity_id);
CREATE INDEX activity_sources_user_provider_idx ON activity_sources (user_id, provider);
CREATE INDEX activity_sources_worker_version_idx
    ON activity_sources (source_worker_name, source_worker_version)
    WHERE reimport_status != 'current';
CREATE INDEX activity_sources_status_idx
    ON activity_sources (status)
    WHERE status != 'active';
CREATE INDEX activity_sources_reimport_status_idx
    ON activity_sources (reimport_status)
    WHERE reimport_status != 'current';

-- Heuristic dedup support: range query on start_time.
-- The Core's dedup pipeline runs:
--   1. Exact match on (provider, external_account_id, external_id)
--   2. Heuristic on start_time ±5min + distance/duration tolerance
--   3. Geo-hash of first/last GPS points
-- This index serves step 2.
CREATE INDEX activity_sources_user_start_lookup_idx
    ON activity_sources (user_id, (parsed ->> 'start_time'));

CREATE TRIGGER activity_sources_set_updated_at
    BEFORE UPDATE ON activity_sources
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Now add the deferred FK on activities.primary_stream_source_id.
--
-- DEFERRABLE INITIALLY DEFERRED is critical here: an Activity row carries
-- a reference to one of its ActivitySource rows, but on initial insert the
-- source row doesn't exist yet. With deferred constraint checking the
-- INSERT INTO activities + INSERT INTO activity_sources pair commits
-- successfully within a single transaction regardless of order.
ALTER TABLE activities
    ADD CONSTRAINT activities_primary_stream_source_id_fk
    FOREIGN KEY (primary_stream_source_id)
    REFERENCES activity_sources(id)
    ON DELETE SET NULL
    DEFERRABLE INITIALLY DEFERRED;

-- ----------------------------------------------------------------------------
-- activity_source_previews
--
-- Holds dry-run reimport results. PreviewSourceReimport queues a job that
-- fetches a fresh source payload and writes the computed merge here for
-- the user to inspect. CommitSourcePreview applies it; DiscardSourcePreview
-- removes it. Records are GC'd after expires_at.
-- ----------------------------------------------------------------------------

CREATE TABLE activity_source_previews (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_source_id  uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- The proposed new parsed payload.
    parsed              jsonb NOT NULL,

    -- Computed source-level diff (vs current parsed payload).
    source_diff         jsonb NOT NULL,

    -- Computed merged-Activity-level diff (vs current activities row).
    merged_diff         jsonb NOT NULL,

    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL
);

CREATE INDEX activity_source_previews_source_id_idx
    ON activity_source_previews (activity_source_id);
CREATE INDEX activity_source_previews_expires_at_idx
    ON activity_source_previews (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS activity_source_previews;
ALTER TABLE IF EXISTS activities DROP CONSTRAINT IF EXISTS activities_primary_stream_source_id_fk;
DROP TABLE IF EXISTS activity_sources;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS gear;
-- +goose StatementEnd
