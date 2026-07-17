-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- segments
--
-- Two sources: "strava" (mirrored read-only per-user) and "cairn" (native).
-- PostGIS GEOGRAPHY columns enable cheap ST_DWithin filtering during the
-- matching pipeline. The polyline TEXT column carries the over-the-wire
-- encoded form clients consume.
-- ----------------------------------------------------------------------------

CREATE TABLE segments (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),

    source                      text NOT NULL
                                CHECK (source IN ('strava', 'cairn')),
    -- Strava-mirrored segments carry these; Cairn-native do not.
    external_id                 text,
    external_account_id         uuid REFERENCES external_accounts(id) ON DELETE CASCADE,
    -- Cairn-native segments carry this; Strava mirrors do not.
    owner_user_id               uuid REFERENCES users(id) ON DELETE CASCADE,

    scope                       text NOT NULL DEFAULT 'private'
                                CHECK (scope IN ('private', 'instance')),

    name                        text NOT NULL,
    description                 text NOT NULL DEFAULT '',

    activity_type               text NOT NULL
                                CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout')),

    -- The path as encoded polyline (precision 5 by default). Stored alongside
    -- the PostGIS geometry to avoid recomputation when serving to clients.
    polyline                    text NOT NULL,
    polyline_precision          integer NOT NULL DEFAULT 5
                                CHECK (polyline_precision IN (5, 6)),

    -- Spatial columns. Maintained on insert/update by a trigger that
    -- reconstructs them from the polyline.
    geom                        geography(LINESTRING, 4326) NOT NULL,
    bbox                        geography(POLYGON, 4326) NOT NULL,

    distance_m                  double precision NOT NULL,
    elevation_gain_m            double precision,
    elevation_loss_m            double precision,
    min_elevation_m             double precision,
    max_elevation_m             double precision,
    avg_grade                   double precision,
    max_grade                   double precision,

    climb_category              text CHECK (climb_category IS NULL OR climb_category IN
                                    ('none', 'cat_4', 'cat_3', 'cat_2', 'cat_1', 'hors_categorie')),

    -- Matching tolerances. NULL means use server defaults.
    match_corridor_m            double precision,
    match_start_tolerance_m     double precision,
    match_end_tolerance_m       double precision,
    bidirectional               boolean NOT NULL DEFAULT false,

    starred                     boolean NOT NULL DEFAULT false,

    -- Denormalized stats for list rendering. Maintained by the
    -- ComputeSegmentStats use case after every effort write.
    stats                       jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),

    -- Either source=strava with (external_id, external_account_id, owner_user_id NULL)
    -- or source=cairn with (owner_user_id, external_id NULL, external_account_id NULL).
    CONSTRAINT segments_source_shape_chk CHECK (
        (source = 'strava'
            AND external_id IS NOT NULL
            AND external_account_id IS NOT NULL
            AND owner_user_id IS NULL)
        OR
        (source = 'cairn'
            AND external_id IS NULL
            AND external_account_id IS NULL
            AND owner_user_id IS NOT NULL)
    ),

    -- Strava segments may only be SCOPE_PRIVATE (per Strava ToS).
    CONSTRAINT segments_strava_scope_chk CHECK (
        source != 'strava' OR scope = 'private'
    )
);

-- Unique-per-account for Strava mirrors.
CREATE UNIQUE INDEX segments_strava_external_uniq
    ON segments (external_account_id, external_id)
    WHERE source = 'strava';

-- Spatial GIST index — drives bounding-box search and corridor matching.
CREATE INDEX segments_geom_gist ON segments USING gist (geom);
CREATE INDEX segments_bbox_gist ON segments USING gist (bbox);

CREATE INDEX segments_owner_idx ON segments (owner_user_id)
    WHERE owner_user_id IS NOT NULL;
CREATE INDEX segments_external_account_idx ON segments (external_account_id)
    WHERE external_account_id IS NOT NULL;
CREATE INDEX segments_scope_activity_type_idx ON segments (scope, activity_type);
CREATE INDEX segments_starred_idx ON segments (owner_user_id, external_account_id)
    WHERE starred = true;
CREATE INDEX segments_name_trgm_idx ON segments USING gin (name gin_trgm_ops);

CREATE TRIGGER segments_set_updated_at
    BEFORE UPDATE ON segments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- segment_efforts
--
-- One row per (segment, activity_source, start_offset) triple. Multi-source
-- activities can produce multiple efforts per segment (one per source's
-- stream) — they are retained for source-comparison views; ranks are
-- computed against the canonical-source effort.
-- ----------------------------------------------------------------------------

CREATE TABLE segment_efforts (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    segment_id                  uuid NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    activity_id                 uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    activity_source_id          uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    start_time                  timestamptz NOT NULL,
    elapsed_s                   double precision NOT NULL,
    moving_s                    double precision NOT NULL,

    start_offset                integer NOT NULL,
    end_offset                  integer NOT NULL,

    avg_heart_rate_bpm          smallint,
    max_heart_rate_bpm          smallint,
    avg_power_w                 smallint,
    avg_cadence                 smallint,
    avg_speed_mps               double precision,

    -- Ranks are denormalized; recomputed by the ComputeSegmentRanks use case
    -- after every effort write for the segment.
    personal_rank               integer NOT NULL DEFAULT 0,
    instance_rank               integer NOT NULL DEFAULT 0,
    is_personal_record          boolean NOT NULL DEFAULT false,
    is_instance_record          boolean NOT NULL DEFAULT false,

    provider_effort_external_id text,

    created_at                  timestamptz NOT NULL DEFAULT now(),

    -- Dedup: a given source stream contributes at most one effort per
    -- (segment, start_offset). Reimports replace by this key.
    UNIQUE (segment_id, activity_source_id, start_offset)
);

-- Leaderboard sorting — fastest first within a segment.
CREATE INDEX segment_efforts_segment_elapsed_idx
    ON segment_efforts (segment_id, elapsed_s ASC);

-- "My history on this segment" query.
CREATE INDEX segment_efforts_user_segment_elapsed_idx
    ON segment_efforts (user_id, segment_id, elapsed_s ASC);

-- "Efforts on this activity".
CREATE INDEX segment_efforts_activity_idx ON segment_efforts (activity_id);
CREATE INDEX segment_efforts_activity_source_idx ON segment_efforts (activity_source_id);

-- PR / KOM lookups by user.
CREATE INDEX segment_efforts_user_prs_idx
    ON segment_efforts (user_id, segment_id, start_time DESC)
    WHERE is_personal_record = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS segment_efforts;
DROP TABLE IF EXISTS segments;
-- +goose StatementEnd
