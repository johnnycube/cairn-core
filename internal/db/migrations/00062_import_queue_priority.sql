-- Manual prioritisation for import-queue rows — "move to top" on the
-- connections queue view. The processor claims pending items by priority
-- first, then newest item_time; priority 0 (the default) keeps the existing
-- order, and a bump above the account's current pending maximum floats a
-- single row to the front without renumbering the rest.

-- +goose Up
ALTER TABLE import_queue ADD COLUMN priority integer NOT NULL DEFAULT 0;

DROP INDEX import_queue_pending_idx;
CREATE INDEX import_queue_pending_idx
    ON import_queue (external_account_id, priority DESC, item_time DESC)
    WHERE status = 'pending';

-- +goose Down
DROP INDEX import_queue_pending_idx;
CREATE INDEX import_queue_pending_idx
    ON import_queue (external_account_id, item_time DESC)
    WHERE status = 'pending';
ALTER TABLE import_queue DROP COLUMN priority;
