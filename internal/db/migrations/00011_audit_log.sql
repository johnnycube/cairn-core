-- +goose Up
-- +goose NO TRANSACTION
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- audit_entries
--
-- Immutable append-only log of admin and user actions. Hypertable because
-- it grows linearly with usage; older chunks are compressed and retained
-- per instance policy (default 1 year).
--
-- Actor fields are snapshotted (username, ip) so audit rows are
-- self-contained even after the actor is deleted.
-- ----------------------------------------------------------------------------

CREATE TABLE audit_entries (
    id                  uuid NOT NULL DEFAULT uuidv7(),
    ts                  timestamptz NOT NULL DEFAULT now(),

    actor_user_id       uuid,                            -- intentionally not FK; user may be deleted
    actor_username      text,
    actor_pat_id        uuid,                            -- intentionally not FK
    ip_address          inet,
    user_agent_summary  text,

    -- Stable string identifier. Examples:
    --   user.created, user.suspended, user.role_changed,
    --   oidc_client.updated, external_account.deleted,
    --   worker.deregistered, instance_settings.updated,
    --   activity.deleted, auth.login_succeeded, auth.login_failed,
    --   auth.session_revoked, auth.password_changed,
    --   webhook_endpoint.created, ...
    action              text NOT NULL,
    severity            text NOT NULL DEFAULT 'info'
                        CHECK (severity IN ('info', 'warn', 'high')),

    resource_type       text,
    resource_id         text,

    -- Optional structured diff: { field_path: { before, after } }.
    diff                jsonb,
    -- Free-form metadata (reason, geo, request_id, ...).
    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,

    PRIMARY KEY (id, ts)
);

SELECT create_hypertable(
    'audit_entries',
    'ts',
    chunk_time_interval => INTERVAL '30 days',
    if_not_exists => TRUE
);

-- "What did user X do?" — admin investigations.
CREATE INDEX audit_entries_actor_ts_idx
    ON audit_entries (actor_user_id, ts DESC)
    WHERE actor_user_id IS NOT NULL;

-- "Show me history of this resource".
CREATE INDEX audit_entries_resource_ts_idx
    ON audit_entries (resource_type, resource_id, ts DESC)
    WHERE resource_type IS NOT NULL;

-- "Show me all HIGH-severity events" — security review surface.
CREATE INDEX audit_entries_severity_ts_idx
    ON audit_entries (severity, ts DESC)
    WHERE severity = 'high';

-- "Show me all logins" or other action-filtered queries.
CREATE INDEX audit_entries_action_ts_idx
    ON audit_entries (action, ts DESC);

-- Free-text search via trigram.
CREATE INDEX audit_entries_action_trgm_idx
    ON audit_entries USING gin (action gin_trgm_ops);

-- ----------------------------------------------------------------------------
-- Compression.
--
-- Audit data is read-mostly after the first day; compress older than 30d.
-- ----------------------------------------------------------------------------

ALTER TABLE audit_entries SET (
    timescaledb.compress,
    timescaledb.compress_orderby = 'ts DESC',
    timescaledb.compress_segmentby = 'action'
);

SELECT add_compression_policy('audit_entries', INTERVAL '30 days');

-- ----------------------------------------------------------------------------
-- Retention.
--
-- Drop audit chunks older than 1 year by default. Operators can adjust
-- the retention policy with TimescaleDB's `add_retention_policy` /
-- `remove_retention_policy` SQL — we ship a sensible default.
-- ----------------------------------------------------------------------------

SELECT add_retention_policy('audit_entries', INTERVAL '1 year');

-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_entries CASCADE;
-- +goose StatementEnd
