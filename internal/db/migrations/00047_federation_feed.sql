-- Federation: inbound activities (workouts) from remote actors a local user
-- follows, surfaced in that user's home feed alongside local following-feed
-- items. See docs/federation-design.md.

-- +goose Up
CREATE TABLE federation_feed_items (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    recipient_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id         text NOT NULL,                 -- remote author (AP actor id)
    activity_ap_id   text NOT NULL,                 -- the AP object id (dedup)
    published        timestamptz NOT NULL,
    name             text NOT NULL DEFAULT '',      -- title
    summary          text NOT NULL DEFAULT '',      -- plain/short content
    url              text NOT NULL DEFAULT '',      -- link back to the origin
    image_url        text NOT NULL DEFAULT '',      -- course image / attachment
    sport            text NOT NULL DEFAULT '',
    distance_m       double precision,
    duration_s       integer,
    elevation_m      double precision,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (recipient_user_id, activity_ap_id)
);

CREATE INDEX federation_feed_items_recipient_published_idx
    ON federation_feed_items (recipient_user_id, published DESC);

-- +goose Down
DROP TABLE IF EXISTS federation_feed_items;
