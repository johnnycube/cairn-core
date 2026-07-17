// Package db is Cairn's database adapter.
//
// Responsibilities:
//   - Open and tune the pgxpool connection pool.
//   - Embed the SQL migrations and run goose against them.
//   - Provide the schema-version safety check used by `cairn serve` at
//     startup to refuse to run against an outdated or future schema.
//
// The package deliberately does NOT export any domain queries — those live
// in adapter packages under internal/adapter/secondary/postgres, generated
// by sqlc from this package's connection.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	pgxuuid "github.com/vgarvardt/pgx-google-uuid/v5"

	"github.com/johnnycube/cairn-core/internal/db/migrations"
)

// Config holds connection and pool settings. All fields are populated from
// environment variables via envconfig; see internal/config for tags.
type Config struct {
	// URL is the PostgreSQL connection string (postgres://...).
	URL string

	// MaxConns caps the pool size. Default 10.
	MaxConns int32
	// MinConns keeps a baseline of warm connections. Default 2.
	MinConns int32

	// StatementTimeout is applied to every new connection via SET.
	// Default 30s.
	StatementTimeout time.Duration

	// LockTimeout caps how long a query waits for row/table locks before
	// erroring. Default 5s — prevents stuck migrations or runaway transactions.
	LockTimeout time.Duration

	// IdleInTransactionTimeout terminates sessions left idle in a transaction
	// to prevent connection leakage. Default 60s.
	IdleInTransactionTimeout time.Duration

	// ApplicationName is set on connections for visibility in pg_stat_activity.
	// Default "cairn".
	ApplicationName string

	// AutoMigrate, when true, applies pending migrations on `cairn serve` start.
	// Defaults to false. Recommended only for single-node / dev setups; in
	// multi-replica deployments migrations should be run as an explicit step
	// via `cairn migrate up`.
	AutoMigrate bool
}

// Open opens a connection pool with the configured settings. Callers are
// responsible for calling Close on the returned pool.
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.ApplicationName != "" {
		if poolCfg.ConnConfig.RuntimeParams == nil {
			poolCfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		poolCfg.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName
	}

	// Per-connection timeouts. These are set on every new connection in
	// the pool so we don't depend on operator-side postgresql.conf tuning.
	// We also register the google/uuid codec here — pgx v5 ships only
	// pgtype.UUID by default, but the domain layer uses google/uuid.UUID
	// for its typed IDs.
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxuuid.Register(conn.TypeMap())

		if cfg.StatementTimeout > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				"SET statement_timeout = %d", cfg.StatementTimeout.Milliseconds(),
			)); err != nil {
				return fmt.Errorf("set statement_timeout: %w", err)
			}
		}
		if cfg.LockTimeout > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				"SET lock_timeout = %d", cfg.LockTimeout.Milliseconds(),
			)); err != nil {
				return fmt.Errorf("set lock_timeout: %w", err)
			}
		}
		if cfg.IdleInTransactionTimeout > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				"SET idle_in_transaction_session_timeout = %d",
				cfg.IdleInTransactionTimeout.Milliseconds(),
			)); err != nil {
				return fmt.Errorf("set idle_in_transaction_session_timeout: %w", err)
			}
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// ----------------------------------------------------------------------------
// Migrations
// ----------------------------------------------------------------------------

// ErrSchemaTooOld is returned by EnsureSchemaCurrent when the DB is at an
// older version than this binary requires.
var ErrSchemaTooOld = errors.New("database schema is older than this binary requires")

// ErrSchemaTooNew is returned by EnsureSchemaCurrent when the DB is at a
// newer version than this binary supports (i.e. this binary is older than
// the cluster's current schema).
var ErrSchemaTooNew = errors.New("database schema is newer than this binary supports")

// SchemaState describes the relationship between the DB's applied
// migrations and the embedded set.
type SchemaState struct {
	// CurrentVersion is the highest migration version applied to the DB.
	CurrentVersion int64
	// LatestVersion is the highest migration version embedded in this binary.
	LatestVersion int64
	// PendingCount is LatestVersion - CurrentVersion when positive.
	PendingCount int
}

// EnsureSchemaCurrent verifies the DB schema matches what this binary expects.
// Returns nil if current, ErrSchemaTooOld / ErrSchemaTooNew otherwise.
// On ErrSchemaTooOld the caller can choose to auto-migrate or refuse to start.
func EnsureSchemaCurrent(ctx context.Context, pool *pgxpool.Pool) (SchemaState, error) {
	state, err := readSchemaState(ctx, pool)
	if err != nil {
		return state, err
	}
	switch {
	case state.CurrentVersion < state.LatestVersion:
		return state, ErrSchemaTooOld
	case state.CurrentVersion > state.LatestVersion:
		return state, ErrSchemaTooNew
	}
	return state, nil
}

// MigrateUp applies all pending migrations and returns the results, one
// per migration applied (empty slice if already up to date).
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) ([]*goose.MigrationResult, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return provider.Up(ctx)
}

// MigrateDown rolls back the most recently applied migration.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) (*goose.MigrationResult, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return provider.Down(ctx)
}

// MigrateDownTo rolls back to and including the named version.
func MigrateDownTo(ctx context.Context, pool *pgxpool.Pool, version int64) ([]*goose.MigrationResult, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return provider.DownTo(ctx, version)
}

// MigrateRedo rolls back the latest migration and re-applies it. Useful when
// iterating on a migration during development.
//
// goose v3 dropped a dedicated Redo() helper from the Provider — we emulate
// it via Down() + UpByOne(). Both run inside their own transaction so the
// two halves are not atomic against each other; that is acceptable in dev
// iteration but a caller using this against production data should think
// twice.
func MigrateRedo(ctx context.Context, pool *pgxpool.Pool) (*goose.MigrationResult, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := provider.Down(ctx); err != nil {
		return nil, err
	}
	return provider.UpByOne(ctx)
}

// MigrateStatus reports per-migration applied/pending state.
func MigrateStatus(ctx context.Context, pool *pgxpool.Pool) ([]*goose.MigrationStatus, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return provider.Status(ctx)
}

// MigrateVersion returns the highest applied migration version.
func MigrateVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return provider.GetDBVersion(ctx)
}

// ----------------------------------------------------------------------------
// internals
// ----------------------------------------------------------------------------

// newProvider builds a goose Provider over the embedded migration FS. Goose
// requires a *sql.DB; the returned *sql.DB must be closed by the caller (it
// opens its own connections and does NOT touch the app pgxpool).
//
// Migrations run over the SIMPLE query protocol, not pgx's default
// extended/prepared protocol. TimescaleDB continuous-aggregate creation
// (CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous)) fails under the
// extended protocol with "continuous aggregate view must include a valid time
// bucket function" — TimescaleDB's ProcessUtility hook needs the raw statement
// text, which it only sees under the simple protocol. We open a dedicated
// *sql.DB from a copy of the pool's connection config with the exec mode
// overridden, so the app pool's runtime queries keep the extended protocol.
func newProvider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	connCfg := pool.Config().ConnConfig.Copy()
	connCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	db := stdlib.OpenDB(*connCfg)
	provider, err := goose.NewProvider(database.DialectPostgres, db, migrations.FS)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("build goose provider: %w", err)
	}
	return provider, db, nil
}

func readSchemaState(ctx context.Context, pool *pgxpool.Pool) (SchemaState, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return SchemaState{}, err
	}
	defer db.Close()

	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return SchemaState{}, fmt.Errorf("read current schema version: %w", err)
	}

	sources := provider.ListSources()
	if len(sources) == 0 {
		return SchemaState{}, fmt.Errorf("no embedded migrations found — binary built incorrectly")
	}

	latest := sources[len(sources)-1].Version

	pending := 0
	for _, s := range sources {
		if s.Version > current {
			pending++
		}
	}

	return SchemaState{
		CurrentVersion: current,
		LatestVersion:  latest,
		PendingCount:   pending,
	}, nil
}
