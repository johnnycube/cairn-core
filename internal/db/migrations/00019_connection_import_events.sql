-- +goose Up

-- connection_import_events is a per-connection (external_account) import history:
-- a durable log of notable import events — full-sync starts, individual activity
-- imports (any path: full sync, reconcile, webhook), and failures. The UI shows
-- the recent tail per connection. Volume is one row per imported activity, which
-- is small and prunable.
CREATE TABLE connection_import_events (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    external_account_id  uuid NOT NULL REFERENCES external_accounts(id) ON DELETE CASCADE,

    -- Event kind: 'sync_started' | 'activity_imported' | 'activity_updated' | 'failed'.
    kind                 text NOT NULL CHECK (kind ~ '^[a-z_]+$'),

    -- count is meaningful for batch events (e.g. sync_started = N queued).
    count                int  NOT NULL DEFAULT 0,

    -- Human-readable one-liner (e.g. the activity title, or an error reason).
    detail               text NOT NULL DEFAULT '',

    -- Optional external id of the activity this event concerns.
    external_id          text NOT NULL DEFAULT '',

    occurred_at          timestamptz NOT NULL DEFAULT now()
);

-- Cursor for the per-connection history view (newest first).
CREATE INDEX connection_import_events_account_time_idx
    ON connection_import_events (external_account_id, occurred_at DESC);

-- +goose Down
DROP TABLE connection_import_events;
