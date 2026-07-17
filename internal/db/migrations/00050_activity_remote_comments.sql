-- +goose Up
-- Remote (federated) comments: a Create{Note, inReplyTo=<local activity>} from a
-- remote ActivityPub actor. Parallel to activity_comments (which FKs local
-- users) so the local path is untouched; the read path merges the two, sorted
-- by time. note_ap_id is the AP Note id, used for idempotent insert + Delete.
CREATE TABLE activity_remote_comments (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id     uuid        NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    remote_actor_id text        NOT NULL,
    note_ap_id      text        NOT NULL,
    body            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    UNIQUE (activity_id, note_ap_id)
);
CREATE INDEX activity_remote_comments_activity_idx
    ON activity_remote_comments (activity_id, created_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE activity_remote_comments;
