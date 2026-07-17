-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- best_efforts
--
-- Sliding-window peaks computed at activity-import time from each source's
-- stream. Standard windows are wired in (5s, 30s, 1min, 5min, 8min, 20min,
-- 60min for power/HR; 400m, 1k, 1mi, 5k, 10k, HM, Marathon for pace).
-- Custom windows from custom_best_efforts append to the computation set.
--
-- Per activity-source: one row per (metric, window_kind, window_value).
-- Multi-source activities therefore yield multiple rows per effort
-- definition — the source with the highest-priority stream wins for the
-- canonical record (see user_records below).
-- ----------------------------------------------------------------------------

CREATE TABLE best_efforts (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id         uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    activity_source_id  uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    activity_type       text NOT NULL
                        CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout')),
    -- Snapshot of the activity's discipline at the time the effort was
    -- computed. Filtered queries ("fastest 5k on track") use this column;
    -- if the user later changes the activity's discipline, the effort is
    -- recomputed by RecomputeBestEffortsForActivity.
    discipline          text,

    metric              text NOT NULL
                        CHECK (metric IN ('pace','speed','power','heart_rate','vam')),
    window_kind         text NOT NULL
                        CHECK (window_kind IN ('distance','duration')),
    -- For distance windows: meters (e.g. 5000 for 5k).
    -- For duration windows: seconds (e.g. 300 for 5min).
    window_value        integer NOT NULL CHECK (window_value > 0),

    -- The achieved value over the window. Units per metric:
    --   pace        : seconds per km
    --   speed       : meters per second
    --   power       : watts
    --   heart_rate  : bpm
    --   vam         : meters per hour
    achieved_value      double precision NOT NULL,

    -- Stream offsets where the effort starts and ends within the source's stream.
    start_offset        integer NOT NULL,
    duration_s          double precision NOT NULL,
    distance_m          double precision,

    -- Activity-absolute timestamp where the effort started — used for
    -- "PR over time" charts.
    ts                  timestamptz NOT NULL,

    created_at          timestamptz NOT NULL DEFAULT now(),

    -- One canonical effort per (source, metric, window) — reimports replace.
    UNIQUE (activity_source_id, metric, window_kind, window_value)
);

-- Per-user records query: walks rows in achieved_value order within
-- (activity_type, metric, window). PACE prefers SMALLER values (faster);
-- application orders accordingly.
CREATE INDEX best_efforts_user_lookup_idx
    ON best_efforts (user_id, activity_type, metric, window_kind, window_value, achieved_value);

CREATE INDEX best_efforts_user_discipline_lookup_idx
    ON best_efforts (user_id, activity_type, discipline, metric, window_kind, window_value, achieved_value)
    WHERE discipline IS NOT NULL;

-- "Power curve for this ride" / "Pace curve for this run".
CREATE INDEX best_efforts_activity_metric_idx
    ON best_efforts (activity_id, metric, window_value);

CREATE INDEX best_efforts_user_ts_idx
    ON best_efforts (user_id, activity_type, metric, ts DESC);

-- ----------------------------------------------------------------------------
-- user_records
--
-- Denormalized "current best" per (user, activity_type, discipline?, metric,
-- window_kind, window_value). Maintained by ComputeUserRecord whenever a
-- best_effort is written that would beat the current record.
--
-- discipline = NULL means "across all disciplines of this activity_type".
-- We hold both rows: the per-discipline best and the cross-discipline best,
-- with COALESCE-based primary key to allow NULL in the key.
-- ----------------------------------------------------------------------------

CREATE TABLE user_records (
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_type       text NOT NULL,
    -- COALESCE'd into the PK; '' means "cross-discipline aggregate".
    discipline_key      text NOT NULL DEFAULT '',
    metric              text NOT NULL,
    window_kind         text NOT NULL,
    window_value        integer NOT NULL,

    achieved_value      double precision NOT NULL,
    activity_id         uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    activity_source_id  uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    best_effort_id      uuid NOT NULL REFERENCES best_efforts(id) ON DELETE CASCADE,
    ts                  timestamptz NOT NULL,

    -- For PR-improvement notifications.
    previous_value      double precision,
    previous_ts         timestamptz,

    updated_at          timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, activity_type, discipline_key, metric, window_kind, window_value)
);

CREATE INDEX user_records_user_type_metric_idx
    ON user_records (user_id, activity_type, metric, window_kind, window_value, ts DESC);

CREATE TRIGGER user_records_set_updated_at
    BEFORE UPDATE ON user_records
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- custom_best_efforts
--
-- User-defined window definitions, appended to the standard computation set
-- per activity type.
-- ----------------------------------------------------------------------------

CREATE TABLE custom_best_efforts (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_type   text NOT NULL
                    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout')),
    metric          text NOT NULL
                    CHECK (metric IN ('pace','speed','power','heart_rate','vam')),
    window_kind     text NOT NULL
                    CHECK (window_kind IN ('distance','duration')),
    window_value    integer NOT NULL CHECK (window_value > 0),
    label           text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (user_id, activity_type, metric, window_kind, window_value)
);

CREATE INDEX custom_best_efforts_user_idx ON custom_best_efforts (user_id, activity_type);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_best_efforts;
DROP TABLE IF EXISTS user_records;
DROP TABLE IF EXISTS best_efforts;
-- +goose StatementEnd
