-- +goose Up

-- Per-user display-format preferences, applied site-wide on every client.
-- units + locale already exist on users; add explicit date/time format choices.
-- Empty string = follow the user's locale default.
ALTER TABLE users
    ADD COLUMN date_format text NOT NULL DEFAULT '' CHECK (date_format IN ('', 'iso', 'us', 'eu')),
    ADD COLUMN time_format text NOT NULL DEFAULT '' CHECK (time_format IN ('', '24h', '12h'));

-- +goose Down
ALTER TABLE users DROP COLUMN date_format, DROP COLUMN time_format;
