-- User blocking + content moderation (multi-user v1). Blocking is the social
-- safety primitive: a blocked user loses all visibility of and interaction with
-- the blocker. Reports feed an admin moderation queue.

-- +goose Up
CREATE TABLE user_blocks (
    blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);
CREATE INDEX user_blocks_blocked_idx ON user_blocks (blocked_id);

CREATE TABLE content_reports (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    reporter_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- target: either an activity, a comment, or a user.
    target_kind  text NOT NULL CHECK (target_kind IN ('activity', 'comment', 'user')),
    target_id    uuid NOT NULL,
    reason       text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewed', 'dismissed')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    reviewed_at  timestamptz,
    reviewed_by  uuid REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX content_reports_status_idx ON content_reports (status, created_at DESC);

-- Admin "hide" of an activity (independent of owner privacy / soft-delete).
ALTER TABLE activities ADD COLUMN hidden_by_admin boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE activities DROP COLUMN hidden_by_admin;
DROP TABLE content_reports;
DROP TABLE user_blocks;
