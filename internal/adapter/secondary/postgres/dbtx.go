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
	"fmt"
	"math"

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

// pgClampInt16 converts v to int16, saturating at the type bounds.
func pgClampInt16(v int) int16 {
	return int16(max(math.MinInt16, min(v, math.MaxInt16)))
}

// pgClampInt32 converts v to int32, saturating at the type bounds.
func pgClampInt32(v int) int32 {
	return int32(max(math.MinInt32, min(v, math.MaxInt32)))
}

// pgCollectRows scans every row with scan and closes rows; error texts are prefixed with scanMsg / iterMsg.
func pgCollectRows[T any](rows pgx.Rows, scan func(rowScanner) (T, error), scanMsg, iterMsg string) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", scanMsg, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", iterMsg, err)
	}
	return out, nil
}
