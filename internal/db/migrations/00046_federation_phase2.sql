-- Federation phase 2: inbound inbox (HTTP-signature-verified), remote follows,
-- and delivery. See docs/federation-design.md.

-- +goose Up

-- Cache of remote ActivityPub actors we've fetched (their inbox + public key,
-- needed to verify their signatures and deliver back to them).
CREATE TABLE federation_actors (
    actor_id          text PRIMARY KEY,           -- the AP actor URL
    inbox             text NOT NULL,
    shared_inbox      text,
    public_key_pem    text NOT NULL,
    preferred_username text,
    domain            text,
    fetched_at        timestamptz NOT NULL DEFAULT now()
);

-- Cross-instance follow edges. direction=inbound: a remote actor follows a
-- local user (we are the followee). direction=outbound: a local user follows a
-- remote actor (later phase).
CREATE TABLE federation_follows (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    local_user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    remote_actor_id    text NOT NULL REFERENCES federation_actors(actor_id) ON DELETE CASCADE,
    direction          text NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    status             text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'accepted')),
    follow_activity_id text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (local_user_id, remote_actor_id, direction)
);

CREATE INDEX federation_follows_followers_idx
    ON federation_follows (local_user_id) WHERE direction = 'inbound' AND status = 'accepted';

-- Dedup: ActivityPub delivery is at-least-once, so we drop already-seen
-- activity ids. Rows are pruned by a periodic sweep (older than a few days).
CREATE TABLE federation_inbox_seen (
    activity_id text PRIMARY KEY,
    seen_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS federation_inbox_seen;
DROP TABLE IF EXISTS federation_follows;
DROP TABLE IF EXISTS federation_actors;
