-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- worker_sessions
--
-- Currently registered worker processes. A worker_id is issued at Register
-- and discarded at Deregister or after missing heartbeats. The same
-- worker_name may appear with multiple worker_id rows when the process
-- restarts; only one session per worker_name is considered HEALTHY at a
-- time (others are OFFLINE / DEGRADED).
-- ----------------------------------------------------------------------------

CREATE TABLE worker_sessions (
    worker_id               uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Stable across restarts and versions ("strava-worker").
    worker_name             text NOT NULL,
    version                 text NOT NULL,
    manifest_hash           text NOT NULL,
    sdk_name                text NOT NULL,
    sdk_version             text NOT NULL,

    -- Full registered manifest (cairn.worker.v1.WorkerManifest jsonb).
    -- Provider list, config_fields, oauth config, supported_jobs all live here.
    manifest                jsonb NOT NULL,

    -- Address the Core can reach for follow-up RPCs. Optional.
    callback_address        text,

    -- Liveness state — updated by heartbeat handler.
    liveness                text NOT NULL DEFAULT 'healthy'
                            CHECK (liveness IN ('healthy', 'degraded', 'offline')),

    -- Job/activity stats.
    active_jobs             integer NOT NULL DEFAULT 0,
    available_capacity      integer NOT NULL DEFAULT 0,
    telemetry               jsonb NOT NULL DEFAULT '{}'::jsonb,

    registered_at           timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at       timestamptz NOT NULL DEFAULT now(),
    deregistered_at         timestamptz
);

CREATE INDEX worker_sessions_worker_name_idx
    ON worker_sessions (worker_name)
    WHERE deregistered_at IS NULL;

CREATE INDEX worker_sessions_liveness_idx
    ON worker_sessions (liveness)
    WHERE deregistered_at IS NULL;

CREATE INDEX worker_sessions_last_heartbeat_idx
    ON worker_sessions (last_heartbeat_at)
    WHERE deregistered_at IS NULL;

-- ----------------------------------------------------------------------------
-- worker_providers
--
-- Flattened (worker_session, provider_id) pairs derived from the manifest.
-- Maintained by an application-layer trigger after every Register; lets the
-- Core resolve provider -> worker_name routing with a simple lookup
-- (no jsonb path-traversal in hot path).
-- ----------------------------------------------------------------------------

CREATE TABLE worker_providers (
    worker_id               uuid NOT NULL REFERENCES worker_sessions(worker_id) ON DELETE CASCADE,
    provider_id             text NOT NULL,
    -- Snapshot of capability flags from the manifest, for quick filter:
    --   "the rate-limit budget is per-account; which providers support webhooks?"
    supports_webhooks       boolean NOT NULL DEFAULT false,
    supports_backfill       boolean NOT NULL DEFAULT false,
    supports_local_reparse  boolean NOT NULL DEFAULT false,
    default_poll_schedule   text,

    PRIMARY KEY (worker_id, provider_id)
);

CREATE INDEX worker_providers_provider_idx ON worker_providers (provider_id);

-- ----------------------------------------------------------------------------
-- provider_assignments
--
-- Admin-controlled mapping from provider_id -> primary worker_name.
-- The Core's job dispatcher uses this to break ties when multiple workers
-- register the same provider. Per-account overrides via
-- external_accounts.assigned_worker_name take precedence.
-- ----------------------------------------------------------------------------

CREATE TABLE provider_assignments (
    provider_id             text PRIMARY KEY,
    primary_worker_name     text NOT NULL,
    -- Audit fields.
    assigned_by_user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    assigned_at             timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER provider_assignments_set_updated_at
    BEFORE UPDATE ON provider_assignments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- worker_registration_history
--
-- Append-only log of worker_session lifecycle events. Survives session
-- deletion so the admin can see "how many times has strava-worker
-- restarted today". Useful for operational visibility independent of the
-- audit log (which is for user-action audit, not worker telemetry).
-- ----------------------------------------------------------------------------

CREATE TABLE worker_registration_history (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    worker_name         text NOT NULL,
    worker_id           uuid NOT NULL,
    event               text NOT NULL
                        CHECK (event IN ('registered', 'deregistered',
                                         'heartbeat_missed', 'offline_marked',
                                         'reregistered_after_offline')),
    version             text,
    manifest_hash       text,
    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,
    ts                  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX worker_registration_history_name_ts_idx
    ON worker_registration_history (worker_name, ts DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS worker_registration_history;
DROP TABLE IF EXISTS provider_assignments;
DROP TABLE IF EXISTS worker_providers;
DROP TABLE IF EXISTS worker_sessions;
-- +goose StatementEnd
