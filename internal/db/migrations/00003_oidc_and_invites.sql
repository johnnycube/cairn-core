-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- oidc_clients
--
-- Configured by the admin via AdminService. The client_secret is encrypted
-- at the application layer using the instance's master key (envelope
-- encryption); the column stores the ciphertext bytes.
-- ----------------------------------------------------------------------------

CREATE TABLE oidc_clients (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    display_name                text NOT NULL,
    icon_url                    text,
    issuer_url                  text NOT NULL,
    client_id                   text NOT NULL,
    client_secret_encrypted     bytea,                       -- NULL = public client
    scopes                      text[] NOT NULL DEFAULT ARRAY['openid', 'profile', 'email'],
    use_pkce                    boolean NOT NULL DEFAULT true,

    -- Maps claim names to Cairn user fields. Required key: "subject".
    -- Optional: email, email_verified, username, display_name, avatar_url, groups.
    claim_mappings              jsonb NOT NULL DEFAULT '{}'::jsonb,

    auto_provision              boolean NOT NULL DEFAULT false,
    auto_provision_role         text NOT NULL DEFAULT 'user'
                                CHECK (auto_provision_role IN ('user', 'admin')),

    -- Optional restriction: only users whose ID-token claim claim_restriction_key
    -- has a value in claim_restriction_values may sign in.
    claim_restriction_key       text,
    claim_restriction_values    text[],

    enabled                     boolean NOT NULL DEFAULT true,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),

    UNIQUE (issuer_url, client_id)
);

CREATE INDEX oidc_clients_enabled_idx ON oidc_clients (enabled);

CREATE TRIGGER oidc_clients_set_updated_at
    BEFORE UPDATE ON oidc_clients
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- linked_oidc_identities
--
-- A user can link multiple OIDC identities across providers.
-- The (oidc_client_id, subject) pair is unique — same provider sub
-- cannot map to two different Cairn users.
-- ----------------------------------------------------------------------------

CREATE TABLE linked_oidc_identities (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    oidc_client_id  uuid NOT NULL REFERENCES oidc_clients(id) ON DELETE CASCADE,
    subject         text NOT NULL,
    email           citext,
    -- The most recent ID-token claims, for debugging and for displaying
    -- provider profile info in the user's account page.
    last_claims     jsonb NOT NULL DEFAULT '{}'::jsonb,
    linked_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz,

    UNIQUE (oidc_client_id, subject)
);

CREATE INDEX linked_oidc_identities_user_id_idx ON linked_oidc_identities (user_id);

-- ----------------------------------------------------------------------------
-- invites
--
-- code is the short URL-friendly invite code; we store its hash to prevent
-- DB-read disclosure. The plaintext code is shown ONCE at creation and
-- only delivered via the invite email (or copied by the admin).
-- ----------------------------------------------------------------------------

CREATE TABLE invites (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    code_hash           bytea NOT NULL UNIQUE,
    -- Optional preview of the code for the admin UI ("abc123…").
    code_prefix         text NOT NULL,
    email               citext,
    assigned_role       text NOT NULL DEFAULT 'user'
                        CHECK (assigned_role IN ('user', 'admin')),
    created_by_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz,
    used_at             timestamptz,
    used_by_user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_at          timestamptz
);

CREATE INDEX invites_email_idx ON invites (email) WHERE used_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invites_expires_at_idx ON invites (expires_at) WHERE used_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invites_created_by_idx ON invites (created_by_user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS linked_oidc_identities;
DROP TABLE IF EXISTS oidc_clients;
-- +goose StatementEnd
