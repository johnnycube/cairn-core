-- Kudos + comments on activities (multi-user v1). Both gated behind the
-- 'social' visibility category at the read/write path.

-- +goose Up
CREATE TABLE activity_kudos (
    activity_id uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (activity_id, user_id)
);
CREATE INDEX activity_kudos_activity_idx ON activity_kudos (activity_id);

CREATE TABLE activity_comments (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);
CREATE INDEX activity_comments_activity_idx ON activity_comments (activity_id, created_at)
    WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE activity_comments;
DROP TABLE activity_kudos;
