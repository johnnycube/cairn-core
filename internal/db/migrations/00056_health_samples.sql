-- Non-activity health time-series (HRV, Sleep, Weight, Steps, WaterIntake,
-- RestingHR): raw per-provider samples, merged per (user, data_type, day) by the
-- provider cascade. Build-ahead — no current worker emits these.

-- +goose Up
CREATE TABLE health_samples (
    id                  uuid NOT NULL DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- capability.DataType identifier: "HRV", "Sleep", "Weight", "Steps",
    -- "WaterIntake", "RestingHR".
    data_type           text NOT NULL,
    ts                  timestamptz NOT NULL,
    provider            text NOT NULL DEFAULT '',
    external_account_id uuid REFERENCES external_accounts(id) ON DELETE SET NULL,
    external_id         text NOT NULL DEFAULT '',
    value               double precision,
    value_struct        jsonb,
    unit                text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- Composite PK: hypertables require the time column in the PK.
    PRIMARY KEY (id, ts),
    CONSTRAINT health_samples_value_present_chk CHECK (
        value IS NOT NULL OR value_struct IS NOT NULL
    )
);

SELECT create_hypertable(
    'health_samples',
    'ts',
    chunk_time_interval => INTERVAL '90 days',
    if_not_exists => TRUE
);

-- Dedup for worker imports: one row per (user, type, instant, provider, ext id).
CREATE UNIQUE INDEX health_samples_natural_key
    ON health_samples (user_id, data_type, ts, provider, external_id);

CREATE INDEX health_samples_lookup
    ON health_samples (user_id, data_type, ts DESC);

-- +goose Down
DROP TABLE IF EXISTS health_samples;
