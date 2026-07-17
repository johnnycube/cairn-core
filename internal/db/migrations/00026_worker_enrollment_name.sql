-- +goose Up

-- Worker identity is now an admin-defined NAME plus the provider it serves.
-- The composite "worker key" {name}-{provider} (e.g. worker1-strava) is the
-- routing/identity token used in NATS subjects and the webhook URL. The name
-- is admin-defined (NOT self-set by the worker) and must be NATS-subject-safe.
--
-- Replaces the old worker_name_pattern glob: the name is now exact, and the
-- worker must present CAIRN_WORKER_NAME == enrollment.name at connect.
ALTER TABLE worker_enrollments
    ADD COLUMN name text NOT NULL DEFAULT ''
        CHECK (name ~ '^[a-z0-9-]*$');

-- Backfill existing rows: default the name to the provider so the composite
-- key is well-formed for any pre-existing enrollment. (Dev only — nothing in
-- prod yet; new enrollments supply an explicit name.)
UPDATE worker_enrollments SET name = provider WHERE name = '';

-- +goose Down

ALTER TABLE worker_enrollments DROP COLUMN IF EXISTS name;
