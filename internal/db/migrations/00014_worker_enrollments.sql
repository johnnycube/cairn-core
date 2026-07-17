-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- worker_enrollments
--
-- One row per "the admin pre-authorized a worker to connect". The actual
-- enrollment token is never stored in plaintext — only sha256(token).
--
-- The token is shown to the admin exactly once at creation time. From
-- then on, the worker reconstructs the hash from its ENV var and the
-- server compares hashes during auth-callout.
--
-- Single-use vs. multi-use is governed by max_uses:
--   * max_uses = 1   → single-use bootstrap token (high security)
--   * max_uses > 1   → can be reused, e.g., for N replicas of the same worker
--   * max_uses = 0   → unlimited reuse (until expires_at or revocation)
-- ----------------------------------------------------------------------------

CREATE TABLE worker_enrollments (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash              bytea NOT NULL UNIQUE,    -- sha256(token), 32 bytes

    -- Provider this token is valid for. The auth-callout will mint a JWT
    -- scoped to subjects ending in `.<provider>` only. NULL is reserved
    -- for an "any-provider" admin-test token; not used in production flows.
    provider                text NOT NULL
                            CHECK (provider ~ '^[a-z0-9_-]+$'),

    -- Worker-name pattern. The auth-callout enforces that the worker's
    -- client-name (passed in CONNECT options) matches this glob. '*'
    -- means "any name for this provider".
    worker_name_pattern     text NOT NULL DEFAULT '*',

    -- Lifecycle
    created_at              timestamptz NOT NULL DEFAULT now(),
    created_by_user_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    note                    text NOT NULL DEFAULT '',

    expires_at              timestamptz NOT NULL,
    max_uses                int NOT NULL DEFAULT 1
                            CHECK (max_uses >= 0),
    uses                    int NOT NULL DEFAULT 0
                            CHECK (uses >= 0),

    revoked_at              timestamptz,
    revoked_by_user_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_reason          text NOT NULL DEFAULT '',

    -- After-the-fact metadata: what NATS account/permissions template should
    -- the auth-callout use when minting the JWT? In v1 the template is
    -- derived purely from `provider` plus the worker name; the column is
    -- here for future per-enrollment custom permissions (e.g. limited
    -- subjects for an evaluation worker).
    permission_template     text NOT NULL DEFAULT 'standard'
);

CREATE INDEX worker_enrollments_active_idx
    ON worker_enrollments (expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX worker_enrollments_provider_idx
    ON worker_enrollments (provider);

-- ----------------------------------------------------------------------------
-- worker_credential_grants
--
-- One row per "we admitted a worker into NATS using a User JWT we minted".
-- This is the audit trail of every successful auth-callout — both for
-- operator visibility and for revocation (we keep the worker's user-nkey
-- public key so we can reject future re-auths from that key).
--
-- The user-JWT itself is reconstructable from `enrollment_id`,
-- `user_nkey_public`, and `expires_at` so we don't store it. The
-- worker holds it in-process; we only need the public key for revocation.
-- ----------------------------------------------------------------------------

CREATE TABLE worker_credential_grants (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    enrollment_id           uuid NOT NULL REFERENCES worker_enrollments(id) ON DELETE RESTRICT,

    -- The auth-callout reads/creates a row in `worker_sessions` for the
    -- (worker_name, instance_id) pair. worker_id is the link.
    -- NULL until the worker_sessions row is created (same-tx in the auth handler).
    worker_id               uuid REFERENCES worker_sessions(worker_id) ON DELETE CASCADE,

    -- Ed25519 user public key the worker generated. Used for revocation:
    -- a row here with revoked_at IS NOT NULL means the auth-callout
    -- refuses future re-auths from this nkey even if the underlying
    -- enrollment is still valid.
    user_nkey_public        text NOT NULL UNIQUE
                            CHECK (user_nkey_public ~ '^U[A-Z0-9]{55}$'),

    -- Client metadata reported during CONNECT
    worker_name             text NOT NULL,
    worker_instance_id      text NOT NULL,
    worker_version          text NOT NULL DEFAULT '',
    client_host             inet,

    issued_at               timestamptz NOT NULL DEFAULT now(),
    expires_at              timestamptz NOT NULL,
    last_seen_at            timestamptz,

    revoked_at              timestamptz,
    revoked_by_user_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_reason          text NOT NULL DEFAULT ''
);

CREATE INDEX worker_credential_grants_enrollment_idx
    ON worker_credential_grants (enrollment_id);
CREATE INDEX worker_credential_grants_worker_idx
    ON worker_credential_grants (worker_id);
CREATE INDEX worker_credential_grants_active_idx
    ON worker_credential_grants (expires_at)
    WHERE revoked_at IS NULL;

-- ----------------------------------------------------------------------------
-- instance_signing_keys
--
-- Cairn-server holds ONE long-lived NATS account signing key. Used to
-- sign every worker user-JWT during auth-callout. Stored encrypted at
-- the application layer (AES-GCM with CAIRN_TOKEN_ENCRYPTION_KEY).
--
-- Rotation: a second row with `active=true` can be added; auth-callout
-- always signs with the latest active key. NATS server is configured
-- with both account public keys for an overlap period during rotation.
-- ----------------------------------------------------------------------------

CREATE TABLE instance_signing_keys (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),

    -- Purpose identifies what subsystem signs with this key. Currently:
    --   * 'nats_account'  -- Account NKey, signs user JWTs for auth-callout
    -- Future:
    --   * 'webhook_egress' -- HMAC for outgoing webhook payloads
    --   * 'export_tokens'  -- Short-lived signed download URLs
    purpose                 text NOT NULL
                            CHECK (purpose IN ('nats_account')),

    -- NKey public form, e.g. "AABC...". Non-secret, used by NATS-server
    -- config to trust this account.
    public_key              text NOT NULL UNIQUE,

    -- Encrypted seed (the private key). Decrypted in-process at
    -- signing time, never logged.
    seed_encrypted          bytea NOT NULL,

    -- Only one row per purpose can be active. Enforced by partial index below.
    active                  boolean NOT NULL DEFAULT true,

    created_at              timestamptz NOT NULL DEFAULT now(),
    created_by_user_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    deactivated_at          timestamptz
);

CREATE UNIQUE INDEX instance_signing_keys_one_active_per_purpose
    ON instance_signing_keys (purpose)
    WHERE active = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS instance_signing_keys;
DROP TABLE IF EXISTS worker_credential_grants;
DROP TABLE IF EXISTS worker_enrollments;

-- +goose StatementEnd
