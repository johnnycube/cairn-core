package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/db"
)

// newMigrateCmd builds the `cairn migrate` subtree. All subcommands share
// the same config-loading and pool-opening logic via withPool.
func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage the database schema (goose-backed, embedded migrations)",
		Long: `Manage the database schema.

Migrations are embedded into this binary. The list is fixed at build time;
upgrade the binary to make new migrations available.

In multi-replica deployments run "cairn migrate up" as a one-shot job
during the upgrade rollout. In single-node deployments you may instead
set CAIRN_DATABASE_AUTO_MIGRATE=true and let "cairn serve" apply them
on startup.`,
	}

	cmd.AddCommand(
		newMigrateUpCmd(),
		newMigrateDownCmd(),
		newMigrateDownToCmd(),
		newMigrateRedoCmd(),
		newMigrateStatusCmd(),
		newMigrateVersionCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------------
// up
// ---------------------------------------------------------------------------

func newMigrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				t0 := time.Now()
				results, err := db.MigrateUp(ctx, pool)
				if err != nil {
					return fmt.Errorf("migrate up: %w", err)
				}
				if len(results) == 0 {
					fmt.Println("schema already up to date")
					return nil
				}
				for _, r := range results {
					fmt.Printf("applied %s in %s\n", baseName(r.Source.Path), r.Duration)
				}
				fmt.Printf("\n%d migration(s) applied in %s\n", len(results), time.Since(t0))
				return nil
			})
		},
	}
}

// ---------------------------------------------------------------------------
// down (one step)
// ---------------------------------------------------------------------------

func newMigrateDownCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Roll back the most recently applied migration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return fmt.Errorf("destructive rollback requires --force")
			}
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				result, err := db.MigrateDown(ctx, pool)
				if err != nil {
					return fmt.Errorf("migrate down: %w", err)
				}
				if result == nil {
					fmt.Println("no migrations to roll back")
					return nil
				}
				fmt.Printf("rolled back %s in %s\n", baseName(result.Source.Path), result.Duration)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "confirm the destructive rollback")
	return cmd
}

// ---------------------------------------------------------------------------
// down-to <version>
// ---------------------------------------------------------------------------

func newMigrateDownToCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "down-to <version>",
		Short: "Roll back to (and including) the named migration version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("parse version: %w", err)
			}
			if !force {
				return fmt.Errorf("destructive rollback requires --force")
			}
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				results, err := db.MigrateDownTo(ctx, pool, version)
				if err != nil {
					return fmt.Errorf("migrate down-to %d: %w", version, err)
				}
				if len(results) == 0 {
					fmt.Println("nothing to roll back")
					return nil
				}
				for _, r := range results {
					fmt.Printf("rolled back %s in %s\n", baseName(r.Source.Path), r.Duration)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "confirm the destructive rollback")
	return cmd
}

// ---------------------------------------------------------------------------
// redo
// ---------------------------------------------------------------------------

func newMigrateRedoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "redo",
		Short: "Roll back the latest migration and re-apply it",
		Long: `Roll back the latest applied migration and apply it again.

Intended for iterating on a migration during development. Not safe for
production data — the down step is destructive.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				result, err := db.MigrateRedo(ctx, pool)
				if err != nil {
					return fmt.Errorf("migrate redo: %w", err)
				}
				if result == nil {
					fmt.Println("no migrations to redo")
					return nil
				}
				fmt.Printf("redid %s in %s\n", baseName(result.Source.Path), result.Duration)
				return nil
			})
		},
	}
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func newMigrateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List every embedded migration and its applied state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				statuses, err := db.MigrateStatus(ctx, pool)
				if err != nil {
					return fmt.Errorf("migrate status: %w", err)
				}

				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "VERSION\tNAME\tSTATE\tAPPLIED AT")
				for _, s := range statuses {
					state := "pending"
					appliedAt := "—"
					if s.State == goose.StateApplied {
						state = "applied"
						if !s.AppliedAt.IsZero() {
							appliedAt = s.AppliedAt.Format(time.RFC3339)
						}
					}
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
						s.Source.Version, baseName(s.Source.Path), state, appliedAt)
				}
				return w.Flush()
			})
		},
	}
}

// ---------------------------------------------------------------------------
// version
// ---------------------------------------------------------------------------

func newMigrateVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the highest migration version currently applied",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), func(ctx context.Context, pool *pgxpool.Pool) error {
				v, err := db.MigrateVersion(ctx, pool)
				if err != nil {
					return fmt.Errorf("migrate version: %w", err)
				}
				fmt.Printf("%d\n", v)
				return nil
			})
		},
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// withPool loads config, opens the pool, runs fn, and closes the pool.
// Every migrate subcommand follows this pattern.
func withPool(ctx context.Context, fn func(context.Context, *pgxpool.Pool) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, db.Config{
		URL:                      cfg.Database.URL,
		MaxConns:                 cfg.Database.MaxConns,
		MinConns:                 cfg.Database.MinConns,
		StatementTimeout:         cfg.Database.StatementTimeout,
		LockTimeout:              cfg.Database.LockTimeout,
		IdleInTransactionTimeout: cfg.Database.IdleInTransactionTimeout,
		ApplicationName:          cfg.Database.ApplicationName,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	return fn(ctx, pool)
}

// baseName trims the directory prefix off a goose-reported migration path
// for compact CLI output ("00007_segments.sql" not "/full/path/00007_segments.sql").
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
