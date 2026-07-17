-- +goose Up

-- WebAuthn / passkey credentials. One row per registered authenticator. The
-- full go-webauthn Credential (public key, AAGUID, sign counter, transports,
-- flags) is stored opaquely as JSON so the domain/port layers stay free of the
-- library type; credential_id is broken out for the O(1) lookup the login
-- ceremony needs.
CREATE TABLE webauthn_credentials (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id bytea NOT NULL UNIQUE,
    credential    jsonb NOT NULL,
    name          text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);

CREATE INDEX webauthn_credentials_user_idx ON webauthn_credentials (user_id);

-- +goose Down
DROP TABLE IF EXISTS webauthn_credentials;
