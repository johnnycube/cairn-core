-- Gear becomes a real entity (docs/gear-v0.4.0.md): provider refs, per-type
-- defaults, who decided an activity's gear, and derived totals.

-- +goose Up
ALTER TABLE gear
    ADD COLUMN retired_at timestamptz,
    ADD COLUMN activity_count bigint NOT NULL DEFAULT 0;
ALTER TABLE gear RENAME COLUMN total_duration_s TO total_moving_s;

-- A provider's gear record. gear_id NULL = pending: the user has not yet
-- decided whether it is new gear or an alias of existing gear; provider_import
-- keeps the provider's description for that decision.
CREATE TABLE gear_external_refs (
    gear_id             uuid REFERENCES gear(id) ON DELETE CASCADE,
    provider            text NOT NULL,
    external_account_id uuid NOT NULL REFERENCES external_accounts(id) ON DELETE CASCADE,
    external_id         text NOT NULL,
    provider_name       text NOT NULL DEFAULT '',
    provider_distance_m double precision,
    is_primary          boolean NOT NULL DEFAULT false,
    provider_import     jsonb NOT NULL DEFAULT '{}',
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, external_account_id, external_id)
);
CREATE INDEX gear_external_refs_gear_idx ON gear_external_refs (gear_id);
CREATE INDEX gear_external_refs_pending_idx ON gear_external_refs (external_account_id)
    WHERE gear_id IS NULL;

CREATE TABLE gear_defaults (
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_type text NOT NULL,
    gear_id       uuid NOT NULL REFERENCES gear(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, activity_type)
);

ALTER TABLE activities
    ADD COLUMN gear_set_by text NOT NULL DEFAULT 'merge'
        CHECK (gear_set_by IN ('merge', 'user'));
-- Gear attached before this release was always a user choice.
UPDATE activities SET gear_set_by = 'user' WHERE gear_id IS NOT NULL;
CREATE INDEX activities_user_gear_idx ON activities (user_id, gear_id)
    WHERE deleted_at IS NULL AND gear_id IS NOT NULL;

-- +goose Down
DROP INDEX activities_user_gear_idx;
ALTER TABLE activities DROP COLUMN gear_set_by;
DROP TABLE gear_defaults;
DROP TABLE gear_external_refs;
ALTER TABLE gear RENAME COLUMN total_moving_s TO total_duration_s;
ALTER TABLE gear DROP COLUMN activity_count, DROP COLUMN retired_at;
