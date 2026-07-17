-- Activity share links (multi-user v1). An unguessable token grants read-only,
-- session-less access to one activity, projected through the 'link' audience.

-- +goose Up
CREATE TABLE activity_share_links (
    token       text PRIMARY KEY,
    activity_id uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    created_by  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX activity_share_links_activity_idx ON activity_share_links (activity_id);

-- +goose Down
DROP TABLE activity_share_links;
