-- Per-user quotas (multi-tenant v1). A NULL override means "use the instance
-- default"; an explicit 0 means "unlimited for this user". Enforced at the
-- user-facing upload path; surfaced to the user and to admins.

-- +goose Up
CREATE TABLE user_quotas (
    user_id        uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_activities integer,        -- NULL = instance default, 0 = unlimited
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE user_quotas;
