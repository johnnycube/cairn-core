-- +goose Up

-- user_provider_configs holds a user's OWN credentials/config for a provider
-- worker (e.g. their Strava API app's client_id/client_secret). Worker configs
-- are per-user, not per-instance: each user brings their own OAuth app, and the
-- worker does that user's work with that user's credentials. The OAuth connect
-- flow reads these instead of any global env.
CREATE TABLE user_provider_configs (
    id                       uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id                  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider                 text NOT NULL CHECK (provider ~ '^[a-z0-9_-]+$'),

    -- OAuth app identity. client_id is not secret; the secret is encrypted at
    -- the app layer (AES-GCM via auth.SecretBox).
    client_id                text NOT NULL DEFAULT '',
    client_secret_encrypted  bytea,

    -- Non-secret provider config field values (keyed by manifest config_field
    -- key) — endpoint overrides, regional URLs, etc.
    config                   jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),

    -- One config per (user, provider).
    UNIQUE (user_id, provider)
);

CREATE INDEX user_provider_configs_user_idx ON user_provider_configs (user_id);

-- +goose Down
DROP TABLE user_provider_configs;
