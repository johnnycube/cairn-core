-- +goose Up

-- A user may now have MANY connections per provider, each with its own OAuth
-- app credentials. Drop the one-per-(user,provider) unique constraint and add a
-- human label. The row id (already the PK) identifies a connection.
ALTER TABLE user_provider_configs
    DROP CONSTRAINT IF EXISTS user_provider_configs_user_id_provider_key;

ALTER TABLE user_provider_configs
    ADD COLUMN IF NOT EXISTS label text NOT NULL DEFAULT '';

-- Link each connected external account to the connection that produced it, so
-- the token-refresh path resolves THAT connection's credentials.
ALTER TABLE external_accounts
    ADD COLUMN IF NOT EXISTS connection_id uuid
        REFERENCES user_provider_configs(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS external_accounts_connection_idx
    ON external_accounts (connection_id);

-- Backfill: link each existing account to its user's (currently single)
-- connection for the same provider, so token refresh keeps working.
UPDATE external_accounts ea
SET connection_id = upc.id
FROM user_provider_configs upc
WHERE ea.connection_id IS NULL
  AND upc.user_id = ea.user_id
  AND upc.provider = ea.provider;

-- +goose Down
ALTER TABLE external_accounts DROP COLUMN IF EXISTS connection_id;
ALTER TABLE user_provider_configs DROP COLUMN IF EXISTS label;
