-- Per-activity user classification overlay. Holds literal user-set values for
-- the classification fields, applied AFTER the merge on every recompute so a
-- user can correct a single field without freezing the rest of the merge or
-- adding a synthetic source. NULL column = "not overridden, use merged value".

-- +goose Up
CREATE TABLE activity_classification_overrides (
    activity_id    uuid PRIMARY KEY REFERENCES activities(id) ON DELETE CASCADE,
    type           text
                   CHECK (type IS NULL OR type IN ('ride','run','swim','hike','walk','row','ski','workout',
                          'snowboard','skate','kayak','sup','surf','golf','climb','tennis','elliptical','wheelchair')),
    discipline     text,
    is_virtual     boolean,
    is_ebike       boolean,
    is_commute     boolean,
    is_race        boolean,
    custom_subtype text,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS activity_classification_overrides;
