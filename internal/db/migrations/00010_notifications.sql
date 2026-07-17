-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- notification_events
--
-- One row per delivered event for one user. Coalescing happens at write
-- time: if an event with the same (user_id, type, dedup_key) exists within
-- the coalescing window, the existing row is updated (coalesce_count +=1,
-- i18n_params merged) instead of inserting a new one.
-- ----------------------------------------------------------------------------

CREATE TABLE notification_events (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Stored as the proto enum integer (NotificationEventType). The
    -- string view is reconstructed from generated code in the app layer.
    type                        integer NOT NULL,

    severity                    text NOT NULL
                                CHECK (severity IN ('info', 'warn', 'error')),

    title_i18n_key              text NOT NULL,
    body_i18n_key               text NOT NULL,
    i18n_params                 jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Optional context references. Nullable; the UI uses these for deep-linking.
    activity_id                 uuid REFERENCES activities(id) ON DELETE SET NULL,
    segment_id                  uuid REFERENCES segments(id) ON DELETE SET NULL,
    external_account_id         uuid REFERENCES external_accounts(id) ON DELETE SET NULL,
    worker_name                 text,

    dedup_key                   text NOT NULL DEFAULT '',
    coalesce_count              integer NOT NULL DEFAULT 1,

    read                        boolean NOT NULL DEFAULT false,
    read_at                     timestamptz,

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

-- Unread badge query — hot path.
CREATE INDEX notification_events_user_unread_idx
    ON notification_events (user_id, created_at DESC)
    WHERE read = false;

-- Coalescing lookup — exists-check within window.
CREATE INDEX notification_events_user_dedup_idx
    ON notification_events (user_id, type, dedup_key, created_at DESC)
    WHERE dedup_key != '';

-- Notification feed.
CREATE INDEX notification_events_user_created_at_idx
    ON notification_events (user_id, created_at DESC);

-- Stream-replay-on-reconnect lookup.
CREATE INDEX notification_events_user_id_uuid_idx
    ON notification_events (user_id, id);

CREATE TRIGGER notification_events_set_updated_at
    BEFORE UPDATE ON notification_events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- notification_preferences
--
-- Sparse matrix: a row exists only when the user has overridden the default
-- for that (event_type, channel) pair. The application resolves missing
-- rows to defaults per the spec in notification.proto.
-- ----------------------------------------------------------------------------

CREATE TABLE notification_preferences (
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type      integer NOT NULL,
    channel         text NOT NULL
                    CHECK (channel IN ('in_app', 'email', 'webhook', 'push')),
    enabled         boolean NOT NULL,
    min_severity    text NOT NULL DEFAULT 'info'
                    CHECK (min_severity IN ('info', 'warn', 'error')),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, event_type, channel)
);

CREATE TRIGGER notification_preferences_set_updated_at
    BEFORE UPDATE ON notification_preferences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- notification_quiet_hours
--
-- Per-user quiet-hours window. Stored as minute offsets in the user's local
-- timezone (resolved via users.timezone). Email and webhook deliveries are
-- queued during quiet hours and dispatched at end-of-window, except for
-- severity=error which always delivers immediately.
-- ----------------------------------------------------------------------------

CREATE TABLE notification_quiet_hours (
    user_id         uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled         boolean NOT NULL DEFAULT false,
    start_minute    integer NOT NULL DEFAULT 1320  -- 22:00
                    CHECK (start_minute >= 0 AND start_minute < 1440),
    end_minute      integer NOT NULL DEFAULT 420   -- 07:00
                    CHECK (end_minute >= 0 AND end_minute < 1440),
    -- 0=Sunday, 6=Saturday. Empty array = every day.
    days_of_week    integer[] NOT NULL DEFAULT '{}'::integer[],
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER notification_quiet_hours_set_updated_at
    BEFORE UPDATE ON notification_quiet_hours
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- webhook_endpoints
--
-- User-defined webhook receivers. The HMAC signing secret is stored
-- encrypted at the application layer because outgoing dispatch needs the
-- plaintext (cannot hash like for inbound auth tokens).
-- ----------------------------------------------------------------------------

CREATE TABLE webhook_endpoints (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                        text NOT NULL,
    url                         text NOT NULL,

    -- Encrypted HMAC-SHA256 secret. App-layer encryption with instance master key.
    signing_secret_encrypted    bytea NOT NULL,
    signing_secret_rotated_at   timestamptz NOT NULL DEFAULT now(),

    -- Empty array = subscribe to all event types.
    event_types                 integer[] NOT NULL DEFAULT '{}'::integer[],
    min_severity                text NOT NULL DEFAULT 'info'
                                CHECK (min_severity IN ('info', 'warn', 'error')),

    enabled                     boolean NOT NULL DEFAULT true,

    -- Delivery telemetry, maintained by the dispatcher.
    last_delivery_at            timestamptz,
    last_delivery_status_code   smallint,
    last_delivery_error         text,
    consecutive_failures        integer NOT NULL DEFAULT 0,

    -- Auto-disabled after N consecutive failures. User must Reenable.
    auto_disabled               boolean NOT NULL DEFAULT false,
    auto_disabled_at            timestamptz,

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX webhook_endpoints_user_id_idx ON webhook_endpoints (user_id);
CREATE INDEX webhook_endpoints_enabled_idx ON webhook_endpoints (enabled)
    WHERE enabled = true AND auto_disabled = false;

CREATE TRIGGER webhook_endpoints_set_updated_at
    BEFORE UPDATE ON webhook_endpoints
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------------------
-- notification_deliveries
--
-- One row per outbound delivery attempt. Used by the admin debug view and
-- for retry scheduling. Old rows are pruned by a maintenance job.
-- ----------------------------------------------------------------------------

CREATE TABLE notification_deliveries (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    event_id                    uuid NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    channel                     text NOT NULL
                                CHECK (channel IN ('in_app', 'email', 'webhook', 'push')),
    webhook_endpoint_id         uuid REFERENCES webhook_endpoints(id) ON DELETE SET NULL,
    email_address               citext,

    status                      text NOT NULL
                                CHECK (status IN (
                                    'queued',
                                    'sent',
                                    'failed_retryable',
                                    'failed_permanent',
                                    'suppressed_quiet_hours',
                                    'suppressed_preference'
                                )),
    http_status_code            smallint,
    error_message               text,
    attempt                     integer NOT NULL DEFAULT 1,
    attempted_at                timestamptz NOT NULL DEFAULT now(),
    next_retry_at               timestamptz
);

CREATE INDEX notification_deliveries_event_idx
    ON notification_deliveries (event_id);
CREATE INDEX notification_deliveries_webhook_idx
    ON notification_deliveries (webhook_endpoint_id, attempted_at DESC)
    WHERE webhook_endpoint_id IS NOT NULL;
CREATE INDEX notification_deliveries_retry_idx
    ON notification_deliveries (next_retry_at)
    WHERE status = 'failed_retryable' AND next_retry_at IS NOT NULL;
CREATE INDEX notification_deliveries_user_attempted_idx
    ON notification_deliveries (user_id, attempted_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
DROP TABLE IF EXISTS notification_quiet_hours;
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS notification_events;
-- +goose StatementEnd
