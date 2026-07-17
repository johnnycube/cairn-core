-- Social follow graph (multi-user v1). A directed edge follower → followee.
-- status supports a future follow-request/accept flow for private profiles;
-- v1 auto-accepts (status='accepted').

-- +goose Up
CREATE TABLE follows (
    follower_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      text NOT NULL DEFAULT 'accepted'
                CHECK (status IN ('pending', 'accepted')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX follows_followee_idx ON follows (followee_id) WHERE status = 'accepted';
CREATE INDEX follows_follower_idx ON follows (follower_id) WHERE status = 'accepted';

-- +goose Down
DROP TABLE follows;
