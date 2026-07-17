-- Extend the activity-type CHECK constraints with the new sport set
-- (snowboard, skate, kayak, sup, surf, golf, climb, tennis, elliptical,
-- wheelchair). Mirrors the cairn.v1.ActivityType proto enum and the
-- domain.ActivityType constants. Inline column CHECKs are auto-named
-- {table}_{column}_check by Postgres.

-- +goose Up
ALTER TABLE activities DROP CONSTRAINT activities_type_check;
ALTER TABLE activities ADD CONSTRAINT activities_type_check
    CHECK (type IN ('ride','run','swim','hike','walk','row','ski','workout',
                    'snowboard','skate','kayak','sup','surf','golf','climb','tennis','elliptical','wheelchair'));

ALTER TABLE segments DROP CONSTRAINT segments_activity_type_check;
ALTER TABLE segments ADD CONSTRAINT segments_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout',
                             'snowboard','skate','kayak','sup','surf','golf','climb','tennis','elliptical','wheelchair'));

ALTER TABLE best_efforts DROP CONSTRAINT best_efforts_activity_type_check;
ALTER TABLE best_efforts ADD CONSTRAINT best_efforts_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout',
                             'snowboard','skate','kayak','sup','surf','golf','climb','tennis','elliptical','wheelchair'));

ALTER TABLE custom_best_efforts DROP CONSTRAINT custom_best_efforts_activity_type_check;
ALTER TABLE custom_best_efforts ADD CONSTRAINT custom_best_efforts_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout',
                             'snowboard','skate','kayak','sup','surf','golf','climb','tennis','elliptical','wheelchair'));

-- +goose Down
-- Revert to the original v1 core + tail set. (Rows using a new type would
-- violate the narrowed CHECK; a real downgrade would need to remap them first.)
ALTER TABLE activities DROP CONSTRAINT activities_type_check;
ALTER TABLE activities ADD CONSTRAINT activities_type_check
    CHECK (type IN ('ride','run','swim','hike','walk','row','ski','workout'));

ALTER TABLE segments DROP CONSTRAINT segments_activity_type_check;
ALTER TABLE segments ADD CONSTRAINT segments_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout'));

ALTER TABLE best_efforts DROP CONSTRAINT best_efforts_activity_type_check;
ALTER TABLE best_efforts ADD CONSTRAINT best_efforts_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout'));

ALTER TABLE custom_best_efforts DROP CONSTRAINT custom_best_efforts_activity_type_check;
ALTER TABLE custom_best_efforts ADD CONSTRAINT custom_best_efforts_activity_type_check
    CHECK (activity_type IN ('ride','run','swim','hike','walk','row','ski','workout'));
