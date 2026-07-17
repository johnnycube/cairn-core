-- +goose Up
-- +goose StatementBegin

-- dead_lettered_jobs records messages that exhausted MaxDeliver on a
-- JetStream consumer. NATS itself doesn't persist the message body
-- once Term() or max-deliveries fire — only the advisory event with
-- subject + stream + consumer + delivery count. We subscribe to that
-- advisory ($JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.>) and copy the
-- message contents here so operators can inspect + replay.
--
-- Indexing:
--   - PK on id for replay lookups
--   - (stream, subject) for filtering in the admin endpoint
--   - first_seen_at DESC for "newest failures first" pagination

CREATE TABLE dead_lettered_jobs (
    id                   uuid PRIMARY KEY,

    -- Where the message came from in JetStream.
    stream               text NOT NULL,
    subject              text NOT NULL,
    consumer             text NOT NULL,

    -- The Nats-Msg-Id header (when set) of the failed message. Useful
    -- for cross-referencing with the originating producer's logs.
    msg_id               text,

    -- The message body, captured verbatim from the dead message.
    -- May be nil when the advisory fires before we can fetch the
    -- payload (rare; JetStream usually keeps it for the duration of
    -- the AckWait window).
    payload              bytea,

    -- Headers, JSON-encoded as {string: string}.
    headers              jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Failure metadata.
    delivered_count      integer NOT NULL,
    last_error           text NOT NULL DEFAULT '',

    -- Operator audit / replay state.
    first_seen_at        timestamptz NOT NULL DEFAULT now(),
    last_seen_at         timestamptz NOT NULL DEFAULT now(),
    replayed_at          timestamptz,
    replayed_by_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    replay_count         integer NOT NULL DEFAULT 0,

    -- For dedup when the same job retries-then-dies twice. The
    -- (stream, subject, msg_id) tuple is the natural key.
    UNIQUE (stream, subject, msg_id)
);

CREATE INDEX dead_lettered_jobs_first_seen_idx
    ON dead_lettered_jobs (first_seen_at DESC);

CREATE INDEX dead_lettered_jobs_stream_subject_idx
    ON dead_lettered_jobs (stream, subject)
    WHERE replayed_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dead_lettered_jobs;
-- +goose StatementEnd
