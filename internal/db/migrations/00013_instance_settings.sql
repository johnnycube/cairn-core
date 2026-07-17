-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- instance_settings
--
-- Single-row table — enforced by a CHECK on a constant primary-key column.
-- The application creates the row at first start; subsequent updates mutate
-- in place.
-- ----------------------------------------------------------------------------

CREATE TABLE instance_settings (
    -- Single-row sentinel. CHECK guarantees no other row can be inserted.
    id                                  smallint PRIMARY KEY DEFAULT 1
                                        CHECK (id = 1),

    -- Identity / branding
    instance_name                       text NOT NULL DEFAULT 'Cairn',
    instance_description                text NOT NULL DEFAULT '',
    support_email                       citext,
    instance_url                        text NOT NULL DEFAULT 'http://localhost:8080',
    logo_url                            text,
    primary_color                       text NOT NULL DEFAULT '#3b82f6',

    -- Defaults for new users.
    default_locale                      text NOT NULL DEFAULT 'en',
    default_timezone                    text NOT NULL DEFAULT 'UTC',
    default_units                       text NOT NULL DEFAULT 'metric'
                                        CHECK (default_units IN ('metric', 'imperial')),

    -- Registration policy.
    registration_open                   boolean NOT NULL DEFAULT false,
    require_invite                      boolean NOT NULL DEFAULT true,
    allowed_email_domains               text[] NOT NULL DEFAULT '{}'::text[],

    -- Outbound email mirror (the actual adapter selection happens via env;
    -- these columns are populated by the application at start so the admin
    -- UI can show "SMTP configured" without re-reading env).
    email_adapter                       text NOT NULL DEFAULT 'none'
                                        CHECK (email_adapter IN ('none', 'smtp', 'ses', 'resend', 'mailgun')),
    email_configured                    boolean NOT NULL DEFAULT false,
    email_from_address                  citext,

    -- Storage mirror.
    storage_adapter                     text NOT NULL DEFAULT 'minio'
                                        CHECK (storage_adapter IN ('minio', 'aws_s3', 'gcs', 'r2', 'local_fs')),
    storage_configured                  boolean NOT NULL DEFAULT false,

    -- Per-user limits.
    per_user_storage_quota_bytes        bigint,                              -- NULL = unlimited
    per_user_max_external_accounts      integer,                             -- NULL = unlimited

    updated_at                          timestamptz NOT NULL DEFAULT now()
);

-- Seed the singleton row immediately so application code can always do
-- SELECT ... FROM instance_settings WHERE id = 1 without a NULL check.
INSERT INTO instance_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TRIGGER instance_settings_set_updated_at
    BEFORE UPDATE ON instance_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS instance_settings;
-- +goose StatementEnd
