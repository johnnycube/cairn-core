-- +goose Up

-- Athlete physiology profile: time-series of physiological values (FTP, weight,
-- threshold HR, …). Each row is one dated measurement; calculations resolve a
-- value AS OF a date by interpolating between bracketing rows. One row per
-- (user, metric, day) — re-saving the same day updates in place.
CREATE TABLE athlete_metrics (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    metric_key     text NOT NULL,
    effective_date date NOT NULL,
    value          double precision NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, metric_key, effective_date)
);

-- Lookup pattern is "all rows for a user, grouped by key, ordered by date".
CREATE INDEX athlete_metrics_user_key_date_idx
    ON athlete_metrics (user_id, metric_key, effective_date);

-- +goose Down
DROP TABLE IF EXISTS athlete_metrics;
