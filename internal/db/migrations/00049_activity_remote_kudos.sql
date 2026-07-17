-- +goose Up
-- Remote (federated) kudos: a Like delivered from a remote ActivityPub actor on
-- a local public activity. Kept separate from activity_kudos (which FKs local
-- users) so the local path is untouched; the read path unions the two counts.
-- like_activity_id stores the AP Like id for observability (Undo matches on the
-- (activity, actor) pair, which is the primary key).
CREATE TABLE activity_remote_kudos (
    activity_id      uuid        NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    remote_actor_id  text        NOT NULL,
    like_activity_id text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (activity_id, remote_actor_id)
);
CREATE INDEX activity_remote_kudos_activity_idx ON activity_remote_kudos (activity_id);

-- +goose Down
DROP TABLE activity_remote_kudos;
