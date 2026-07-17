// Package postgres is the Postgres-backed secondary adapter. It implements
// the repository ports declared in internal/port using pgx and the schema
// defined by the embedded migrations (internal/db/migrations).
//
// Conventions
// -----------
//
//   - Each repository holds a *pgxpool.Pool. Per-call, dbtx(ctx, pool)
//     returns either the active transaction (when called inside InTx)
//     or the pool. Repositories therefore have no Tx parameter in their
//     method signatures.
//
//   - jsonb columns are encoded/decoded via wire-type structs (suffixed
//     JSON, defined alongside the methods that use them) rather than by
//     tagging domain types. Keeps the domain layer free of persistence
//     concerns.
//
//   - Domain.ErrNotFound is wrapped via fmt.Errorf so callers branch with
//     errors.Is(err, domain.ErrNotFound).
//
//   - No SQL injection risk: every dynamic value is bound as a parameter;
//     no fmt.Sprintf into query strings.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the subset of pgxpool.Pool and pgx.Tx that all repository code
// uses. Defining it explicitly lets dbtx() return a single interface
// regardless of whether we're inside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ctxKey is the type for context keys used by this package. Defining a
// private type prevents collisions with keys from other packages.
type ctxKey int

const (
	// txCtxKey holds the active *pgx.Tx during an InTx block.
	txCtxKey ctxKey = iota
)

// dbtx returns the active transaction from ctx, or pool when no
// transaction is in flight. Every repository method's first line is
// `db := dbtx(ctx, r.pool)`.
func dbtx(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx, ok := ctx.Value(txCtxKey).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// withTx returns a child context carrying tx. Used internally by TxManager.
func withTx(parent context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(parent, txCtxKey, tx)
}

// txFromCtx returns the active transaction from ctx, or (nil, false) when
// no transaction is in flight. Used by TxManager to detect nesting.
func txFromCtx(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txCtxKey).(pgx.Tx)
	return tx, ok
}
