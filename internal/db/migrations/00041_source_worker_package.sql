-- Per-source worker package provenance (update-available trigger). A source is
-- "ready for update" when a newer worker FROM THE SAME PACKAGE for the SAME
-- PROVIDER (higher version) arrives — the routing name/alias is irrelevant, so
-- we key the trigger on (provider, source_worker_package, version). Older rows
-- backfill to '' and simply don't match until re-imported by a package-stamped
-- worker.

-- +goose Up
ALTER TABLE activity_sources
    ADD COLUMN source_worker_package text NOT NULL DEFAULT '';

-- Supports the trigger's WHERE (provider, package, current rows only).
CREATE INDEX activity_sources_pkg_provider_idx
    ON activity_sources (provider, source_worker_package)
    WHERE reimport_status = 'current';

-- +goose Down
DROP INDEX activity_sources_pkg_provider_idx;
ALTER TABLE activity_sources DROP COLUMN source_worker_package;
