-- +goose Up

-- import_queue is the persisted, core-driven work list for provider imports.
-- After a full-sync discovery, each importable item (an activity, segment,
-- metric, ...) becomes a row here. A core processor goroutine dequeues
-- pending rows and dispatches fetch jobs to the worker, paced and
-- rate-limit-aware (an account's queue pauses while it's rate-limited and
-- resumes when the window resets). State: pending → in_progress →
-- done | failed (| skipped when the user chose to skip already-present items).
CREATE TABLE import_queue (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    external_account_id  uuid NOT NULL REFERENCES external_accounts(id) ON DELETE CASCADE,
    user_id              uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider             text NOT NULL,

    -- What kind of entity this row imports.
    item_type            text NOT NULL CHECK (item_type IN ('activity', 'segment', 'metric')),
    -- The provider's id for the item (the strong dedup key, same one ingest
    -- dedups on).
    external_id          text NOT NULL,
    -- The item's own timestamp (e.g. activity start) for newest-first ordering.
    item_time            timestamptz,

    status               text NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'in_progress', 'done', 'failed', 'skipped')),
    attempts             integer NOT NULL DEFAULT 0,
    last_error           text,

    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    completed_at         timestamptz,

    -- One queue row per (account, type, external_id): discovery re-runs and
    -- overlapping syncs collapse instead of duplicating work.
    UNIQUE (external_account_id, item_type, external_id)
);

-- The processor's hot path: next pending items for an account, newest first.
CREATE INDEX import_queue_pending_idx
    ON import_queue (external_account_id, item_time DESC)
    WHERE status = 'pending';

-- The per-user / per-account queue view in the UI.
CREATE INDEX import_queue_account_status_idx
    ON import_queue (external_account_id, status);
CREATE INDEX import_queue_user_idx
    ON import_queue (user_id, status);

-- +goose Down
DROP TABLE import_queue;
