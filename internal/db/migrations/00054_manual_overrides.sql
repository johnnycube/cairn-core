-- Manual overrides as durable inputs:
-- - field_source_overrides: per (activity, field group) pin which source wins.
-- - source_match_denylist: detached identities that must not re-attach (Gap 6).

-- +goose Up
CREATE TABLE field_source_overrides (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    -- field group identifier (domain.FieldGroup): distance, heart_rate, power,
    -- elevation, gps_track, laps, ...
    field_key   text NOT NULL,
    source_id   uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    decided_by  text NOT NULL DEFAULT 'manual' CHECK (decided_by IN ('manual')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (activity_id, field_key)
);

CREATE INDEX field_source_overrides_activity_idx ON field_source_overrides (activity_id);

CREATE TABLE source_match_denylist (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider            text NOT NULL,
    external_account_id uuid REFERENCES external_accounts(id) ON DELETE SET NULL,
    external_id         text NOT NULL,
    reason              text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- NULLS NOT DISTINCT (PG15+) so a null external_account_id (manual uploads)
    -- still dedups the denylist row.
    UNIQUE NULLS NOT DISTINCT (user_id, provider, external_account_id, external_id)
);

CREATE INDEX source_match_denylist_lookup_idx
    ON source_match_denylist (user_id, provider, external_id);

-- +goose Down
DROP TABLE IF EXISTS source_match_denylist;
DROP TABLE IF EXISTS field_source_overrides;
