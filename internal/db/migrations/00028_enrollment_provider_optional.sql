-- +goose Up

-- Provider (and version) are now WORKER-reported metadata (sent in the
-- heartbeat), not admin-set on the enrollment. The admin only sets a name,
-- expiry, and note. Relax the provider CHECK so an enrollment can be created
-- without one (empty string); keep the column for back-compat of older rows.
ALTER TABLE worker_enrollments
    DROP CONSTRAINT IF EXISTS worker_enrollments_provider_check;
ALTER TABLE worker_enrollments
    ADD CONSTRAINT worker_enrollments_provider_check
        CHECK (provider ~ '^[a-z0-9_-]*$');

-- +goose Down

ALTER TABLE worker_enrollments
    DROP CONSTRAINT IF EXISTS worker_enrollments_provider_check;
ALTER TABLE worker_enrollments
    ADD CONSTRAINT worker_enrollments_provider_check
        CHECK (provider ~ '^[a-z0-9_-]+$');
