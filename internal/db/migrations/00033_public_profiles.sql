-- Opt-in public athlete profiles (multi-user v1). The username doubles as the
-- public slug; a profile is only rendered to anonymous viewers when opted in.

-- +goose Up
ALTER TABLE users ADD COLUMN profile_is_public boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN profile_is_public;
