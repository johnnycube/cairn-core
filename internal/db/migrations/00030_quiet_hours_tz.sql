-- Add a timezone to notification quiet hours so the start/end minute window is
-- interpreted in the user's local time, not UTC. Defaults to UTC for existing
-- rows (backward compatible: a user who never set quiet hours has enabled=false
-- anyway, so the tz is moot until they configure it).

-- +goose Up
ALTER TABLE notification_quiet_hours
    ADD COLUMN tz text NOT NULL DEFAULT 'UTC';

-- +goose Down
ALTER TABLE notification_quiet_hours
    DROP COLUMN tz;
