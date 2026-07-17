-- OAuth 2.1 authorization server: clients, authorization codes, and the
-- access/refresh tokens it issues. Cairn was already an OIDC *consumer* (login
-- with external IdPs) and stored provider account tokens in external_accounts;
-- this adds the *authorization server* side so native apps, third-party
-- clients and MCP agents can obtain scoped, revocable access via
-- authorization-code + PKCE. Only token hashes are stored.

-- +goose Up
CREATE TABLE oauth_clients (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     text NOT NULL UNIQUE,
    secret_hash   bytea,                         -- NULL for public (PKCE-only) clients
    name          text NOT NULL,
    redirect_uris text[] NOT NULL DEFAULT '{}',
    grant_types   text[] NOT NULL DEFAULT '{authorization_code,refresh_token}',
    scopes        text[] NOT NULL DEFAULT '{}',  -- scopes this client may request
    token_auth    text NOT NULL DEFAULT 'none',  -- none | client_secret_basic | client_secret_post
    is_dynamic    boolean NOT NULL DEFAULT false, -- created via RFC 7591 DCR
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_authorization_codes (
    code_hash             bytea PRIMARY KEY,
    client_id             text NOT NULL,
    user_id               uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri          text NOT NULL,
    scope                 text NOT NULL DEFAULT '',
    code_challenge        text NOT NULL,
    code_challenge_method text NOT NULL DEFAULT 'S256',
    expires_at            timestamptz NOT NULL,
    consumed_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_access_tokens (
    token_hash bytea PRIMARY KEY,
    client_id  text NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_access_tokens_user ON oauth_access_tokens(user_id);

CREATE TABLE oauth_refresh_tokens (
    token_hash bytea PRIMARY KEY,
    client_id  text NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_refresh_tokens_user ON oauth_refresh_tokens(user_id);

-- +goose Down
DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_clients;
