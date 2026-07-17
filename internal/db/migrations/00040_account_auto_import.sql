-- Per-account auto-import suspend switch (#93). Lets a user pause automatic
-- imports (reconcile + webhook-driven fetches) for one registered account
-- without disconnecting it — tokens, history and config are preserved. A
-- suspended account is simply skipped by the scheduler and the webhook lookup.

-- +goose Up
ALTER TABLE external_accounts
    ADD COLUMN auto_import_enabled boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE external_accounts DROP COLUMN auto_import_enabled;
