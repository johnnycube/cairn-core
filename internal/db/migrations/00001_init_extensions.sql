-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- Extensions
--
-- All schema lives in the default `public` schema; we avoid additional
-- schemas to keep the deployment surface minimal.
-- ----------------------------------------------------------------------------

CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid, digest, etc.
CREATE EXTENSION IF NOT EXISTS citext;          -- case-insensitive text (usernames, emails)
CREATE EXTENSION IF NOT EXISTS pg_trgm;         -- trigram indexes for free-text search
CREATE EXTENSION IF NOT EXISTS btree_gin;       -- composite GIN indexes
CREATE EXTENSION IF NOT EXISTS postgis;         -- spatial types and functions
CREATE EXTENSION IF NOT EXISTS timescaledb;     -- hypertables, continuous aggregates

-- ----------------------------------------------------------------------------
-- UUIDv7 helper
--
-- Postgres 18+ ships a native pg_catalog.uuidv7(). On older versions we
-- install a pure-SQL implementation in public and use it as the DEFAULT for
-- every primary key, so rows are inserted in time-ordered fashion (much
-- better B-tree behavior than v4). Unqualified uuidv7() calls resolve to
-- pg_catalog first, so the native function wins automatically where it
-- exists; the polyfill only fills the gap on < 18.
-- ----------------------------------------------------------------------------

DO $do$
BEGIN
    -- Skip when uuidv7() already exists in public — some environments pre-seed
    -- one (template databases, other tooling), possibly under a different
    -- owner, and both CREATE and CREATE OR REPLACE would fail then.
    IF current_setting('server_version_num')::int < 180000
       AND NOT EXISTS (
           SELECT 1 FROM pg_proc p
           JOIN pg_namespace n ON n.oid = p.pronamespace
           WHERE p.proname = 'uuidv7' AND n.nspname = 'public' AND p.pronargs = 0
       ) THEN
        CREATE FUNCTION public.uuidv7() RETURNS uuid AS $func$
            SELECT encode(
                set_bit(
                    set_bit(
                        overlay(
                            uuid_send(gen_random_uuid())
                            PLACING substring(int8send(floor(extract(epoch from clock_timestamp()) * 1000)::bigint) FROM 3)
                            FROM 1 FOR 6
                        ),
                        52, 1
                    ),
                    53, 1
                ),
                'hex'
            )::uuid;
        $func$ LANGUAGE SQL VOLATILE;

        COMMENT ON FUNCTION public.uuidv7() IS
            'UUIDv7 (RFC 9562): first 48 bits = unix-ms timestamp, remainder random. Time-ordered inserts. Polyfill for Postgres < 18.';
    END IF;
END
$do$;

-- ----------------------------------------------------------------------------
-- updated_at trigger helper
--
-- Most tables have an updated_at column that should auto-refresh on UPDATE.
-- We install one shared trigger function rather than duplicate per-table.
-- ----------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS set_updated_at();
DROP FUNCTION IF EXISTS public.uuidv7();
-- We do NOT drop extensions on Down — they may be in use elsewhere, and
-- the cost of re-creating them on rollback isn't worth the risk.
-- +goose StatementEnd
