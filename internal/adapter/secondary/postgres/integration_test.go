//go:build integration

// Package postgres integration tests run against a REAL Postgres
// (TimescaleDB + PostGIS), not a mock. They are build-tagged `integration` so
// the default `go test ./...` (unit) suite stays fast and DB-free; run them
// with `make test-integration` or `go test -tags integration ./...`.
//
// Point them at a throwaway database via CAIRN_TEST_DATABASE_URL — each test
// runs every embedded migration against it first, so the schema is always
// current. CI provisions a fresh timescale/timescaledb-ha service for this.
package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/db"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// requirePool opens a pool to the test DB and applies all migrations. It skips
// the test (rather than failing) when CAIRN_TEST_DATABASE_URL is unset, so the
// integration suite is a no-op on machines without a database.
func requirePool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("CAIRN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CAIRN_TEST_DATABASE_URL unset — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return ctx, pool
}

// TestUserRepo_RoundTrip is the harness proof-of-life: a fresh DB + the full
// embedded migration set + a users round-trip through the real repo, including
// the citext case-insensitive email lookup. Richer per-repo tests hang off the
// same requirePool.
func TestUserRepo_RoundTrip(t *testing.T) {
	ctx, pool := requirePool(t)
	repo := NewUserRepo(pool)

	id := domain.UserID(uuid.New())
	suffix := id.String()[:8]
	u := domain.User{
		ID:          id,
		Username:    "itest-" + suffix,
		Email:       "itest-" + suffix + "@example.com",
		DisplayName: "Integration Test",
		Role:        domain.UserRoleUser,
		Status:      domain.UserStatusActive,
		Units:       domain.UserUnitsMetric,
	}
	cred := domain.Credentials{UserID: id, PasswordHash: "x"}

	if err := repo.CreateUser(ctx, u, cred); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := repo.GetUser(ctx, id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Username != u.Username || got.Email != u.Email {
		t.Fatalf("round-trip mismatch: got %q/%q want %q/%q",
			got.Username, got.Email, u.Username, u.Email)
	}
	if got.Role != domain.UserRoleUser || got.Status != domain.UserStatusActive {
		t.Fatalf("role/status not persisted: %q/%q", got.Role, got.Status)
	}

	// citext email column → case-insensitive lookup must find the same row.
	byEmail, err := repo.GetUserByEmail(ctx, "ITEST-"+suffix+"@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("GetUserByEmail (case-insensitive): %v", err)
	}
	if byEmail.ID != id {
		t.Fatalf("email lookup returned wrong user")
	}
}
