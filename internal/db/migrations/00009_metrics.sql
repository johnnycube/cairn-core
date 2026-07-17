-- +goose Up
-- +goose NO TRANSACTION
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- metrics
--
-- Time-series store for everything that isn't an activity: weight, HRV,
-- steps, sleep, threshold values, training-load rollups. TimescaleDB
-- hypertable on ts; chunked monthly because metric volume is far lower
-- than stream volume.
--
-- value_numeric covers ~95% of metrics. value_struct (jsonb) handles
-- non-scalar shapes (sleep_stages, blood_pressure pair, etc.). The domain
-- enforces per-key shape; the DB only checks "exactly one is set".
--
-- Dedup: a given (user, key, ts, provider, external_id) tuple is unique
-- when external_id is set (idempotent worker imports). Manual entries
-- (provider='manual', external_id=NULL) dedup by id only.
-- ----------------------------------------------------------------------------

CREATE TABLE metrics (
    id                          uuid NOT NULL DEFAULT uuidv7(),
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key                         text NOT NULL,
    ts                          timestamptz NOT NULL,
    period_seconds              integer NOT NULL DEFAULT 0
                                CHECK (period_seconds >= 0),

    value_numeric               double precision,
    value_struct                jsonb,

    provider                    text NOT NULL DEFAULT 'manual',
    external_account_id         uuid REFERENCES external_accounts(id) ON DELETE SET NULL,
    external_id                 text,
    source_worker_name          text,
    source_worker_version       text,

    activity_id                 uuid REFERENCES activities(id) ON DELETE SET NULL,

    tags                        text[] NOT NULL DEFAULT '{}'::text[],
    notes                       text NOT NULL DEFAULT '',

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),

    -- Composite PK because hypertables require ts in the PK.
    PRIMARY KEY (id, ts),

    CONSTRAINT metrics_value_present_chk CHECK (
        value_numeric IS NOT NULL OR value_struct IS NOT NULL
    )
);

SELECT create_hypertable(
    'metrics',
    'ts',
    chunk_time_interval => INTERVAL '30 days',
    if_not_exists => TRUE
);

-- Dedup index for worker imports. NULL external_id rows are excluded.
CREATE UNIQUE INDEX metrics_natural_key_uniq
    ON metrics (user_id, key, ts, provider, external_id)
    WHERE external_id IS NOT NULL;

-- "Show me this metric over time" — the hot path.
CREATE INDEX metrics_user_key_ts_idx
    ON metrics (user_id, key, ts DESC);

-- "Latest value across keys" — dashboard rendering.
CREATE INDEX metrics_user_ts_idx
    ON metrics (user_id, ts DESC);

-- Filter by provider (settings page: "weight from Withings").
CREATE INDEX metrics_user_key_provider_idx
    ON metrics (user_id, key, provider, ts DESC);

-- Activity-linked metrics (TSS for a given ride).
CREATE INDEX metrics_activity_id_idx
    ON metrics (activity_id) WHERE activity_id IS NOT NULL;

CREATE TRIGGER metrics_set_updated_at
    BEFORE UPDATE ON metrics
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- Compression on old chunks.
--
-- Metrics rarely need updating once written; older than 90 days compresses
-- well. Segmenting by user keeps decompression cheap for per-user reads.
-- ----------------------------------------------------------------------------

ALTER TABLE metrics SET (
    timescaledb.compress,
    timescaledb.compress_orderby = 'ts DESC',
    timescaledb.compress_segmentby = 'user_id, key'
);

SELECT add_compression_policy('metrics', INTERVAL '90 days');

-- ----------------------------------------------------------------------------
-- user_settings
--
-- One row per user, created on user creation by the application. All
-- map-typed settings are jsonb objects; the application layer parses
-- them into structured types per UserSettings in proto.
-- ----------------------------------------------------------------------------

CREATE TABLE user_settings (
    user_id                                 uuid PRIMARY KEY
                                            REFERENCES users(id) ON DELETE CASCADE,

    height_cm                               double precision,
    birthdate                               date,
    biological_sex                          text CHECK (biological_sex IS NULL OR biological_sex IN
                                                ('female', 'male', 'other')),
    max_hr_estimate_bpm                     smallint,

    -- Threshold overrides — when set, replace auto-derived values from
    -- the matching metric key.
    ftp_cycling_override_w                  smallint,
    lthr_cycling_override_bpm               smallint,
    lthr_running_override_bpm               smallint,
    threshold_pace_running_override_s_per_km integer,
    css_swimming_override_s_per_100m        integer,

    -- Map<activity_type, ActivityTypeMergePolicy>.
    merge_policy_by_activity_type           jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Map<metric_key, ProviderPriorityList>.
    metric_priority_by_key                  jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Map<provider, AutoReimportPolicy>.
    auto_reimport_by_provider               jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Map<provider, string>  (cron expressions).
    poll_schedule_override_by_provider      jsonb NOT NULL DEFAULT '{}'::jsonb,

    default_activity_privacy                text NOT NULL DEFAULT 'private'
                                            CHECK (default_activity_privacy IN
                                                ('private', 'followers', 'public')),

    -- Map<activity_type, TSSPreference>.
    tss_preference_by_activity_type         jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Map<activity_type, BestEffortWindowSet>.
    best_effort_windows_by_activity_type    jsonb NOT NULL DEFAULT '{}'::jsonb,

    updated_at                              timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER user_settings_set_updated_at
    BEFORE UPDATE ON user_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP TABLE IF EXISTS user_settings;
DROP TABLE IF EXISTS metrics CASCADE;
-- +goose StatementEnd
