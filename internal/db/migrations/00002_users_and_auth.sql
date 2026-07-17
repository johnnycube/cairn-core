-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- users
--
-- Username and email are stored as citext for case-insensitive uniqueness.
-- password_hash is NULL for OIDC-only / passkey-only users.
-- ----------------------------------------------------------------------------

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    username        citext NOT NULL UNIQUE,
    email           citext NOT NULL UNIQUE,
    email_verified  boolean NOT NULL DEFAULT false,
    password_hash   text,                    -- argon2id encoded string; NULL = no password set
    display_name    text NOT NULL,
    avatar_url      text,

    locale          text NOT NULL DEFAULT 'en',
    timezone        text NOT NULL DEFAULT 'UTC',
    units           text NOT NULL DEFAULT 'metric'
                    CHECK (units IN ('metric', 'imperial')),

    role            text NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'admin')),
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'invited', 'suspended', 'deleted')),
    status_reason   text,

    -- Per-user policy state.
    must_change_password boolean NOT NULL DEFAULT false,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_login_at   timestamptz
);

CREATE INDEX users_status_idx ON users (status) WHERE status != 'deleted';
CREATE INDEX users_role_idx ON users (role);
CREATE INDEX users_username_trgm_idx ON users USING gin (username gin_trgm_ops);
CREATE INDEX users_email_trgm_idx ON users USING gin (email gin_trgm_ops);
CREATE INDEX users_display_name_trgm_idx ON users USING gin (display_name gin_trgm_ops);

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- sessions
--
-- Server-side sessions. token_hash is the SHA-256 of the bearer token /
-- cookie value (the plaintext token is never stored).
-- ----------------------------------------------------------------------------

CREATE TABLE sessions (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash          bytea NOT NULL UNIQUE,            -- SHA-256 of bearer token (32 bytes)

    auth_method         text NOT NULL
                        CHECK (auth_method IN (
                            'password',
                            'password_with_webauthn_2fa',
                            'webauthn_passkey',
                            'oidc',
                            'personal_access_token'
                        )),

    user_agent_summary  text,
    ip_address          inet,
    ip_geo_summary      text,

    created_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    revoked_at          timestamptz
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at) WHERE revoked_at IS NULL;

-- ----------------------------------------------------------------------------
-- passkeys (WebAuthn credentials)
-- ----------------------------------------------------------------------------

CREATE TABLE passkeys (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                text NOT NULL,
    credential_id       bytea NOT NULL UNIQUE,
    public_key          bytea NOT NULL,
    sign_count          bigint NOT NULL DEFAULT 0,
    aaguid              uuid,                              -- authenticator model
    transport_hint      text,                              -- "usb", "internal", ...
    backup_eligible     boolean NOT NULL DEFAULT false,
    backup_state        boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_used_at        timestamptz
);

CREATE INDEX passkeys_user_id_idx ON passkeys (user_id);

-- ----------------------------------------------------------------------------
-- personal_access_tokens
--
-- token_hash is SHA-256. The plaintext is shown ONCE at creation.
-- ----------------------------------------------------------------------------

CREATE TABLE personal_access_tokens (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                text NOT NULL,
    token_hash          bytea NOT NULL UNIQUE,
    -- Token prefix shown in admin UI to identify-without-exposing
    -- (first 8 chars of plaintext + "..."). Helps the user find the right token to revoke.
    token_prefix        text NOT NULL,
    scopes              text[] NOT NULL DEFAULT '{}'::text[],
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz,
    revoked_at          timestamptz,
    last_used_at        timestamptz
);

CREATE INDEX personal_access_tokens_user_id_idx
    ON personal_access_tokens (user_id) WHERE revoked_at IS NULL;

-- ----------------------------------------------------------------------------
-- password_reset_tokens & email_verification_tokens
--
-- Short-lived single-use tokens. token_hash is SHA-256.
-- ----------------------------------------------------------------------------

CREATE TABLE password_reset_tokens (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  bytea PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);

CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);
CREATE INDEX password_reset_tokens_expires_at_idx ON password_reset_tokens (expires_at) WHERE used_at IS NULL;

CREATE TABLE email_verification_tokens (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  bytea PRIMARY KEY,
    email       citext NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);

CREATE INDEX email_verification_tokens_user_id_idx ON email_verification_tokens (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS email_verification_tokens;
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS personal_access_tokens;
DROP TABLE IF EXISTS passkeys;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
