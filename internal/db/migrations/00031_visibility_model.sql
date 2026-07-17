-- Per-field visibility model (multi-user v1). See docs/visibility-model.md.
-- The per-user default policy + per-activity override (both jsonb keyed by
-- audience → category list) + privacy zones that mask GPS near home.

-- +goose Up
CREATE TABLE user_visibility_defaults (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- { "public": ["summary"], "followers": ["summary","map",...], "link": [...] }
    policy     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE activities
    ADD COLUMN visibility_override jsonb;

CREATE TABLE privacy_zones (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      text NOT NULL DEFAULT '',
    lat        double precision NOT NULL,
    lng        double precision NOT NULL,
    radius_m   double precision NOT NULL DEFAULT 200,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX privacy_zones_user_idx ON privacy_zones (user_id);

-- +goose Down
DROP TABLE privacy_zones;
ALTER TABLE activities DROP COLUMN visibility_override;
DROP TABLE user_visibility_defaults;
