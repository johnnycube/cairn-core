-- +goose Up

-- Stage-3 dedup queries existing activities by their start coordinate (a
-- bounding box around the incoming activity's first GPS point). user_id leads
-- the btree so the per-user range scan on start_lat/start_lng is index-served.
CREATE INDEX activities_user_start_geo_idx
    ON activities (user_id, start_lat, start_lng)
    WHERE deleted_at IS NULL AND start_lat IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS activities_user_start_geo_idx;
