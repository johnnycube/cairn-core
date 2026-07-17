-- Drop the legacy HTTP worker-registry / session subsystem. Worker presence,
-- manifests and routing now live entirely in NATS KV (cairn_worker_presence,
-- cairn_worker_manifests); the DB tables from migration 12 were only written by
-- the removed workerapi.Service (HTTP Register/Heartbeat/Deregister) and had no
-- live readers. worker_credential_grants.worker_id was the FK link into
-- worker_sessions and was always NULL once the auth-callout stopped creating
-- session rows, so the column goes too.

-- +goose Up
ALTER TABLE worker_credential_grants DROP COLUMN IF EXISTS worker_id;

DROP TABLE IF EXISTS worker_registration_history;
DROP TABLE IF EXISTS provider_assignments;
DROP TABLE IF EXISTS worker_providers;
DROP TABLE IF EXISTS worker_sessions;

-- +goose Down
-- Best-effort structural reverse. The dropped data is not restored (these
-- tables were unused), and the recreated worker_id column is left nullable.
CREATE TABLE worker_sessions (
    worker_id          uuid PRIMARY KEY DEFAULT uuidv7(),
    worker_name        text NOT NULL,
    version            text NOT NULL,
    manifest_hash      text NOT NULL,
    sdk_name           text NOT NULL,
    sdk_version        text NOT NULL,
    manifest           jsonb NOT NULL,
    callback_address   text,
    liveness           text NOT NULL DEFAULT 'healthy'
                       CHECK (liveness IN ('healthy', 'degraded', 'offline')),
    active_jobs        integer NOT NULL DEFAULT 0,
    available_capacity integer NOT NULL DEFAULT 0,
    telemetry          jsonb NOT NULL DEFAULT '{}'::jsonb,
    registered_at      timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at  timestamptz NOT NULL DEFAULT now(),
    deregistered_at    timestamptz
);

CREATE TABLE worker_providers (
    worker_id              uuid NOT NULL REFERENCES worker_sessions(worker_id) ON DELETE CASCADE,
    provider_id            text NOT NULL,
    supports_webhooks      boolean NOT NULL DEFAULT false,
    supports_backfill      boolean NOT NULL DEFAULT false,
    supports_local_reparse boolean NOT NULL DEFAULT false,
    default_poll_schedule  text,
    PRIMARY KEY (worker_id, provider_id)
);

CREATE TABLE provider_assignments (
    provider_id         text PRIMARY KEY,
    primary_worker_name text NOT NULL,
    assigned_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    assigned_at         timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE worker_registration_history (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    worker_name   text NOT NULL,
    worker_id     uuid NOT NULL,
    event         text NOT NULL
                  CHECK (event IN ('registered', 'deregistered',
                                   'heartbeat_missed', 'offline_marked',
                                   'reregistered_after_offline')),
    version       text,
    manifest_hash text,
    metadata      jsonb NOT NULL DEFAULT '{}'::jsonb,
    ts            timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE worker_credential_grants
    ADD COLUMN worker_id uuid REFERENCES worker_sessions(worker_id) ON DELETE CASCADE;
