-- +goose Up
-- Durable outbound ActivityPub delivery queue (federation Phase 3). The push
-- fan-out of a user's activity Create to followers' inboxes was best-effort +
-- logged; this persists each (activity, inbox) delivery so a transient failure
-- (5xx / network / 429) is retried with capped exponential backoff instead of
-- silently dropped. A permanent reject (4xx other than 429) goes straight to
-- 'dead'.
CREATE TABLE federation_deliveries (
    id              uuid PRIMARY KEY,
    from_user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id        text        NOT NULL,            -- signing identity (keyId base)
    inbox_url       text        NOT NULL,
    body            jsonb       NOT NULL,            -- the activity JSON to POST
    activity_ap_id  text        NOT NULL DEFAULT '', -- the object/activity id, for dedup
    status          text        NOT NULL DEFAULT 'pending',
    attempts        int         NOT NULL DEFAULT 0,
    max_attempts    int         NOT NULL DEFAULT 8,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    CONSTRAINT federation_deliveries_status_chk
        CHECK (status IN ('pending', 'delivered', 'dead')),
    -- One delivery per (sender, inbox, activity): re-running the publish step
    -- for the same activity is idempotent.
    CONSTRAINT federation_deliveries_uniq
        UNIQUE (from_user_id, inbox_url, activity_ap_id)
);

-- The scheduler's claim query: pending rows whose retry time has elapsed.
CREATE INDEX federation_deliveries_due_idx
    ON federation_deliveries (next_attempt_at)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE federation_deliveries;
