-- OIDC providers are now configured ENTIRELY in the environment (CAIRN_OIDC_*)
-- and held in memory — no DB client storage, no admin-UI editing. Drop the
-- oidc_clients table and re-key linked identities on the provider id string
-- (linked_oidc_identities.provider). Existing links referenced oidc_clients
-- UUIDs with no clean mapping to provider ids, so they're cleared (nothing has
-- shipped to production).

-- +goose Up
DELETE FROM linked_oidc_identities;

-- Dropping the column also drops its FK to oidc_clients and the
-- UNIQUE(oidc_client_id, subject) it participated in.
ALTER TABLE linked_oidc_identities DROP COLUMN oidc_client_id;
ALTER TABLE linked_oidc_identities ADD COLUMN provider text NOT NULL DEFAULT '';
ALTER TABLE linked_oidc_identities ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE linked_oidc_identities
    ADD CONSTRAINT linked_oidc_identities_provider_subject_key UNIQUE (provider, subject);

-- oauth_authorize_states is vestigial (the live OIDC flow uses a signed cookie,
-- not this table) and FKs to oidc_clients; drop it, then the client table.
DROP TABLE IF EXISTS oauth_authorize_states;
DROP TABLE IF EXISTS oidc_clients;

-- +goose Down
-- Best-effort structural reverse (the env-declared provider config can't be
-- restored, and previously-linked identities were cleared).
CREATE TABLE oidc_clients (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    display_name            text NOT NULL,
    icon_url                text,
    issuer_url              text NOT NULL,
    client_id               text NOT NULL,
    client_secret_encrypted bytea,
    scopes                  text[] NOT NULL DEFAULT ARRAY['openid','profile','email'],
    use_pkce                boolean NOT NULL DEFAULT true,
    claim_mappings          jsonb NOT NULL DEFAULT '{}'::jsonb,
    auto_provision          boolean NOT NULL DEFAULT false,
    auto_provision_role     text NOT NULL DEFAULT 'user' CHECK (auto_provision_role IN ('user','admin')),
    claim_restriction_key   text,
    claim_restriction_values text[],
    enabled                 boolean NOT NULL DEFAULT true,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer_url, client_id)
);
ALTER TABLE linked_oidc_identities
    DROP CONSTRAINT linked_oidc_identities_provider_subject_key,
    DROP COLUMN provider,
    ADD COLUMN oidc_client_id uuid NOT NULL REFERENCES oidc_clients(id) ON DELETE CASCADE;
ALTER TABLE linked_oidc_identities
    ADD CONSTRAINT linked_oidc_identities_oidc_client_id_subject_key UNIQUE (oidc_client_id, subject);
