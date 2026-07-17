-- +goose Up
-- +goose NO TRANSACTION
-- NB: no StatementBegin/End wrapper. These statements must each run as their
-- own auto-committed exec — CREATE MATERIALIZED VIEW ... (timescaledb.continuous)
-- cannot run inside a transaction block, and wrapping the whole migration in
-- one statement block would put it in an implicit transaction. goose splits on
-- ';' here; none of the statements below contain inner semicolons.

-- ----------------------------------------------------------------------------
-- activity_streams
--
-- Time-series stream samples — one row per (activity_source_id, ts).
-- All channels live in a single wide table; NULL is the default for
-- channels a given source does not provide. TimescaleDB compresses NULL
-- columns efficiently in chunked storage, so wide+sparse is fine.
--
-- ts is the absolute UTC timestamp at which the sample was taken.
-- Activity-relative time is derived in queries via (ts - activities.start_time).
--
-- NOTE: this migration runs OUTSIDE a transaction (see the NO TRANSACTION
-- annotation at the top of the file)
-- because TimescaleDB's create_hypertable, compression policy and
-- continuous aggregate refresh policy emit commands that cannot run
-- inside a user transaction.
--
-- Because each statement auto-commits, a mid-file failure leaves earlier
-- statements applied with no goose version record. Every statement is
-- therefore guarded with IF NOT EXISTS so a retry can pick up where the
-- last run died instead of colliding with its own leftovers.
-- ----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS activity_streams (
    activity_source_id          uuid NOT NULL,
    ts                          timestamptz NOT NULL,

    -- Core channels (almost always present when stream exists).
    latitude                    double precision,
    longitude                   double precision,
    altitude_m                  double precision,
    distance_m                  double precision,           -- cumulative
    speed_mps                   double precision,
    heart_rate_bpm              smallint,
    power_w                     smallint,
    cadence                     smallint,
    temperature_c               real,
    grade                       real,

    -- Cycling-specific advanced metrics.
    left_right_balance          real,
    left_torque_effectiveness   real,
    right_torque_effectiveness  real,
    left_pedal_smoothness       real,
    right_pedal_smoothness      real,

    -- Running-specific advanced metrics.
    vertical_oscillation_mm     real,
    ground_contact_time_ms      smallint,
    stride_length_m             real,

    -- Other.
    respiration_rate_brpm       real,                       -- breaths per minute
    core_temperature_c          real,

    PRIMARY KEY (activity_source_id, ts)
);

-- Convert to TimescaleDB hypertable, chunking by ts at 7-day intervals.
-- Most activities fit within a single chunk, so this balances chunk count
-- vs per-chunk size well.
SELECT create_hypertable(
    'activity_streams',
    'ts',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists => TRUE
);

-- An index on activity_source_id alone is needed because most reads filter
-- by source first and the PK's leading column already provides this — but
-- we add a hash index hint via DESC ordering for typical scrub-from-end
-- queries.
CREATE INDEX IF NOT EXISTS activity_streams_source_ts_idx
    ON activity_streams (activity_source_id, ts DESC);

-- ----------------------------------------------------------------------------
-- Compression on old chunks.
--
-- Streams older than 30 days are compressed. TimescaleDB native compression
-- yields ~10x on time-series data with many similar values; for stream
-- channels with lots of NULLs the ratio is higher still.
-- ----------------------------------------------------------------------------

ALTER TABLE activity_streams SET (
    timescaledb.compress,
    timescaledb.compress_orderby = 'ts DESC',
    timescaledb.compress_segmentby = 'activity_source_id'
);

SELECT add_compression_policy('activity_streams', INTERVAL '30 days', if_not_exists => TRUE);

-- ----------------------------------------------------------------------------
-- Continuous aggregate at 5-second resolution.
--
-- Used by GetActivityStream to serve downsampled views without hitting the
-- raw 1Hz data. Channels are aggregated as follows:
--
--   * GPS (latitude, longitude, altitude): first()  — keeps a real point
--                                                   (averaging coordinates
--                                                   distorts the route).
--   * cumulative distance:                 last()  — monotonically increasing.
--   * scalar metrics (HR, power, ...):     avg()   — smooths spikes.
--
-- Refresh policy: every 1 minute, lagging by 1 minute (recent samples remain
-- live-queryable from the raw table).
-- ----------------------------------------------------------------------------

CREATE MATERIALIZED VIEW IF NOT EXISTS activity_streams_5s
WITH (timescaledb.continuous) AS
SELECT
    activity_source_id,
    time_bucket(INTERVAL '5 seconds', ts)                       AS bucket,
    first(latitude, ts)                                         AS latitude,
    first(longitude, ts)                                        AS longitude,
    first(altitude_m, ts)                                       AS altitude_m,
    last(distance_m, ts)                                        AS distance_m,
    avg(speed_mps)                                              AS speed_mps,
    avg(heart_rate_bpm)::smallint                               AS heart_rate_bpm,
    avg(power_w)::smallint                                      AS power_w,
    avg(cadence)::smallint                                      AS cadence,
    avg(temperature_c)::real                                    AS temperature_c,
    avg(grade)::real                                            AS grade,
    avg(vertical_oscillation_mm)::real                          AS vertical_oscillation_mm,
    avg(ground_contact_time_ms)::smallint                       AS ground_contact_time_ms,
    avg(stride_length_m)::real                                  AS stride_length_m,
    avg(respiration_rate_brpm)::real                            AS respiration_rate_brpm,
    avg(core_temperature_c)::real                               AS core_temperature_c
FROM activity_streams
GROUP BY activity_source_id, bucket
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
    'activity_streams_5s',
    start_offset => INTERVAL '90 days',
    end_offset   => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute',
    if_not_exists => TRUE
);

-- ----------------------------------------------------------------------------
-- Continuous aggregate at 30-second resolution.
--
-- Used for very long activities (multi-hour rides) where 5s is too granular
-- to render in a single chart pass.
--
-- Built directly from the raw hypertable, NOT stacked on activity_streams_5s.
-- A hierarchical CAgg (time_bucket over the 5s aggregate's bucket column) is
-- rejected by TimescaleDB 2.27 with "continuous aggregate view must include a
-- valid time bucket function". Aggregating raw 1Hz into 30s buckets directly
-- is robust across versions; the incremental refresh cost is negligible.
-- ----------------------------------------------------------------------------

CREATE MATERIALIZED VIEW IF NOT EXISTS activity_streams_30s
WITH (timescaledb.continuous) AS
SELECT
    activity_source_id,
    time_bucket(INTERVAL '30 seconds', ts)                      AS bucket,
    first(latitude, ts)                                         AS latitude,
    first(longitude, ts)                                        AS longitude,
    first(altitude_m, ts)                                       AS altitude_m,
    last(distance_m, ts)                                        AS distance_m,
    avg(speed_mps)                                              AS speed_mps,
    avg(heart_rate_bpm)::smallint                               AS heart_rate_bpm,
    avg(power_w)::smallint                                      AS power_w,
    avg(cadence)::smallint                                      AS cadence,
    avg(temperature_c)::real                                    AS temperature_c,
    avg(grade)::real                                            AS grade
FROM activity_streams
GROUP BY activity_source_id, bucket
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
    'activity_streams_30s',
    start_offset => INTERVAL '180 days',
    end_offset   => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists => TRUE
);

-- +goose Down
-- +goose NO TRANSACTION
DROP MATERIALIZED VIEW IF EXISTS activity_streams_30s CASCADE;
DROP MATERIALIZED VIEW IF EXISTS activity_streams_5s CASCADE;
DROP TABLE IF EXISTS activity_streams CASCADE;
