-- Instance-wide merge-priority defaults (cascade: per-user → instance → AnyProvider).
-- Shape mirrors user_settings.merge_policy_by_activity_type.

-- +goose Up
ALTER TABLE instance_settings
    ADD COLUMN merge_defaults_json jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE instance_settings DROP COLUMN merge_defaults_json;
