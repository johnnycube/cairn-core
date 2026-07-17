-- Re-clustering support: activities.match_confidence/needs_review (matcher band);
-- match_constraints (must/cannot-link); activity_id_redirects (merge dissolves an
-- id → redirect to the survivor).
--
-- The activity_id FK is made DEFERRABLE INITIALLY DEFERRED so one re-cluster tx
-- can reassign sources and create activities in any order (validated at COMMIT).

-- +goose Up
ALTER TABLE activity_sources
    DROP CONSTRAINT activity_sources_activity_id_fkey,
    ADD CONSTRAINT activity_sources_activity_id_fkey
        FOREIGN KEY (activity_id) REFERENCES activities(id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE activities
    ADD COLUMN match_confidence text NOT NULL DEFAULT 'high'
        CHECK (match_confidence IN ('high','medium','low')),
    ADD COLUMN needs_review boolean NOT NULL DEFAULT false;

CREATE INDEX activities_needs_review_idx
    ON activities (user_id) WHERE needs_review;

CREATE TABLE match_constraints (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The two source records the decision relates. Ordered (source_a < source_b)
    -- so a pair is stored once regardless of argument order.
    source_a    uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    source_b    uuid NOT NULL REFERENCES activity_sources(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('must_link','cannot_link')),
    reason      text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT match_constraints_order_chk CHECK (source_a < source_b),
    UNIQUE (source_a, source_b)
);

CREATE INDEX match_constraints_user_idx ON match_constraints (user_id);

CREATE TABLE activity_id_redirects (
    old_id     uuid PRIMARY KEY,
    new_id     uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_id_redirects_new_idx ON activity_id_redirects (new_id);

-- Time-window blocking index: the re-cluster engine pulls candidates by
-- (user, start_utc range) and lets the matcher GATE on sport (so synonyms like
-- "Run"/"Running" and wildcard "Workout" still compare). The sport-class index
-- from migration 00052 stays for other lookups.
CREATE INDEX activity_sources_user_start_idx ON activity_sources (user_id, start_utc);

-- +goose Down
DROP INDEX IF EXISTS activity_sources_user_start_idx;
DROP TABLE IF EXISTS activity_id_redirects;
DROP TABLE IF EXISTS match_constraints;
DROP INDEX IF EXISTS activities_needs_review_idx;
ALTER TABLE activities
    DROP COLUMN match_confidence,
    DROP COLUMN needs_review;
ALTER TABLE activity_sources
    DROP CONSTRAINT activity_sources_activity_id_fkey,
    ADD CONSTRAINT activity_sources_activity_id_fkey
        FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE CASCADE;
