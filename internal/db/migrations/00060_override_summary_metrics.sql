-- Extend the per-activity user overlay with summary-metric overrides
-- (distance / elevation gain / moving time). NULL = not overridden, use the
-- merged value. Applied after the merge by RecomputeActivityFromSources.

-- +goose Up
ALTER TABLE activity_classification_overrides
    ADD COLUMN distance_m        double precision,
    ADD COLUMN elevation_gain_m  double precision,
    ADD COLUMN moving_duration_s bigint;

-- +goose Down
ALTER TABLE activity_classification_overrides
    DROP COLUMN distance_m,
    DROP COLUMN elevation_gain_m,
    DROP COLUMN moving_duration_s;
