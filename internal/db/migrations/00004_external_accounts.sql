-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- external_accounts
--
-- One row per (user, provider, provider_account_id). Multiple accounts of
-- the same provider per user are supported (e.g. two Strava accounts).
--
-- Provider tokens and sensitive config values are encrypted at the
-- application layer using the instance master key — columns hold ciphertext.
--
-- The `config` jsonb holds non-secret configuration fields the user filled
-- in based on the worker's ProviderManifest.config_fields (e.g. an OAuth
-- client_id chosen by the user). Secret config field values are stored
-- separately in `config_secrets_encrypted`, also keyed by config field key.
-- ----------------------------------------------------------------------------

CREATE TABLE external_accounts (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider                    text NOT NULL,
    provider_account_id         text,                       -- resolved after first OAuth exchange

    display_label               text NOT NULL,

    -- Worker assignment. Defaults to NULL (use current primary worker for
    -- the provider). Set when an admin pins this account for canary migration.
    assigned_worker_name        text,

    status                      text NOT NULL DEFAULT 'active'
                                CHECK (status IN (
                                    'active',
                                    'auth_invalid',
                                    'worker_offline',
                                    'rate_limited',
                                    'needs_migration',
                                    'disabled'
                                )),
    status_reason               text,

    -- Non-secret config field values, keyed by ConfigField.key from the
    -- manifest. JSON because values may be lists for MULTI_SELECT.
    config                      jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Secret config field values, encrypted at the app layer. Map of
    -- config field key -> ciphertext bytes.
    config_secrets_encrypted    jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- OAuth state (when auth_method = OAUTH2). Tokens are encrypted.
    access_token_encrypted      bytea,
    refresh_token_encrypted     bytea,
    access_token_expires_at     timestamptz,
    granted_scopes              text[] NOT NULL DEFAULT '{}'::text[],

    -- Webhook subscription state.
    webhook_subscribed          boolean NOT NULL DEFAULT false,
    webhook_subscribed_at       timestamptz,
    webhook_subscription_id     text,
    -- Per-account signing token, embedded in the webhook callback URL.
    -- Rotated by admin or by RotateWebhookToken. Stored as a hash; the
    -- plaintext is supplied to the worker at RegisterWebhook job dispatch.
    webhook_signing_token_hash  bytea,
    webhook_signing_token_rotated_at timestamptz,

    -- Rate-limit snapshot reported by the most recent worker call.
    rate_limit                  jsonb,

    -- Watermark for incremental imports. Subsequent IMPORT_ACTIVITIES jobs
    -- use this as `since`.
    sync_watermark              timestamptz,

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    last_sync_at                timestamptz,
    last_successful_sync_at     timestamptz,

    -- Same provider may appear multiple times per user, but the resolved
    -- provider_account_id (e.g. Strava athlete id) must be unique per user.
    -- Enforced via partial unique index so NULLs (pre-OAuth) don't collide.
    CONSTRAINT external_accounts_provider_check
        CHECK (provider ~ '^[a-z0-9_-]+$')
);

CREATE UNIQUE INDEX external_accounts_provider_account_uniq
    ON external_accounts (user_id, provider, provider_account_id)
    WHERE provider_account_id IS NOT NULL;

CREATE INDEX external_accounts_user_id_idx ON external_accounts (user_id);
CREATE INDEX external_accounts_provider_idx ON external_accounts (provider);
CREATE INDEX external_accounts_status_idx ON external_accounts (status)
    WHERE status != 'active';
CREATE INDEX external_accounts_assigned_worker_idx
    ON external_accounts (assigned_worker_name)
    WHERE assigned_worker_name IS NOT NULL;

CREATE TRIGGER external_accounts_set_updated_at
    BEFORE UPDATE ON external_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- oauth_authorize_states
--
-- Short-lived state values for the OAuth dance. Two kinds of flows:
--   * Login / link via OIDC (purpose = 'oidc_login' or 'oidc_link')
--   * External account connection (purpose = 'external_account_authorize')
--
-- The state value is signed and embedded in the OAuth `state` param;
-- here we store the metadata needed to complete the callback.
-- ----------------------------------------------------------------------------

CREATE TABLE oauth_authorize_states (
    state_hash          bytea PRIMARY KEY,
    purpose             text NOT NULL
                        CHECK (purpose IN (
                            'oidc_login',
                            'oidc_link',
                            'external_account_authorize',
                            'external_account_reauthorize'
                        )),
    -- For OIDC flows: the oidc_clients.id. For external accounts: NULL.
    oidc_client_id      uuid REFERENCES oidc_clients(id) ON DELETE CASCADE,
    -- For external account flows: the external_accounts.id (NULL on initial
    -- "Create" flow, set on Reauthorize).
    external_account_id uuid REFERENCES external_accounts(id) ON DELETE CASCADE,
    -- For oidc_link flows: the user_id being augmented.
    user_id             uuid REFERENCES users(id) ON DELETE CASCADE,
    pkce_verifier       text,
    post_login_redirect text,
    -- For external_account_authorize: snapshot of the provider context needed
    -- (provider id, intended worker assignment, etc.).
    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL
);

CREATE INDEX oauth_authorize_states_expires_at_idx
    ON oauth_authorize_states (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS oauth_authorize_states;
DROP TABLE IF EXISTS external_accounts;
-- +goose StatementEnd
