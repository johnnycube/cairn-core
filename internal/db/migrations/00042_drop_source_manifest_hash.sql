-- Retire source_manifest_hash (no longer needed). Update-available and re-parse
-- eligibility now key on (provider, source_worker_package, version) — the
-- maintainer-upheld compatibility contract. The build hash adds nothing.

-- +goose Up
ALTER TABLE activity_sources DROP COLUMN source_manifest_hash;

-- +goose Down
ALTER TABLE activity_sources ADD COLUMN source_manifest_hash text NOT NULL DEFAULT '';
