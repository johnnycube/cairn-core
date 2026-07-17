-- +goose Up

-- Worker version is part of the admission identity (provider, name, version).
-- It is a simple incrementing INTEGER (not a hash — hashes aren't user
-- friendly). Bumping a worker's version makes it a NEW worker that needs a NEW
-- enrollment: the enrollment's token only admits the version it was minted for.
-- The routing key stays {name}-{provider} (no version), so a v1→v2 rolling
-- upgrade still shares the work queue.
ALTER TABLE worker_enrollments
    ADD COLUMN version integer NOT NULL DEFAULT 1
        CHECK (version >= 1);

-- +goose Down

ALTER TABLE worker_enrollments DROP COLUMN IF EXISTS version;
