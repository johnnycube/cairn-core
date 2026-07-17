-- Delegated administration (multi-user v1): a 'moderator' role that grants
-- access to the moderation queue (reports + hide) WITHOUT full admin powers
-- (no user management, no NATS/system config). Full multi-org tenancy with
-- per-org roles remains a v2 concern; this is the bounded v1 cut.

-- +goose Up
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user', 'moderator', 'admin'));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user', 'admin'));
