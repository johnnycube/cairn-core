package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/db"
)

// newRotateKeyCmd re-encrypts every secret-at-rest under a new master key.
//
// Cairn encrypts secrets (OAuth tokens, OIDC + per-user provider client
// secrets, the NATS account signing-key seed) with AES-256-GCM via
// auth.SecretBox, where the master key comes from
// CAIRN_AUTH_MASTER_ENCRYPTION_KEY. Rotating that key means re-encrypting all
// of that ciphertext: decrypt with the OLD key, encrypt with the NEW key,
// preserving each column's AAD (additional authenticated data).
//
// The command is RESUMABLE: a ciphertext that no longer decrypts under the old
// key but DOES under the new key is treated as already-rotated and skipped, so
// re-running after a partial failure is safe. --dry-run validates that the
// current key decrypts everything (no new key, no writes).
//
//	# validate the current key can read every secret:
//	cairn rotate-key --dry-run
//	# rotate (CAIRN_AUTH_MASTER_ENCRYPTION_KEY = old key, --new-key = new):
//	cairn rotate-key --new-key="$(openssl rand -base64 32)"
//
// After rotation, set CAIRN_AUTH_MASTER_ENCRYPTION_KEY to the new key and
// restart the server + workers.
func newRotateKeyCmd() *cobra.Command {
	var newKey string
	var dryRun bool
	var skipUnreadable bool

	cmd := &cobra.Command{
		Use:   "rotate-key",
		Short: "Re-encrypt all secrets-at-rest under a new master encryption key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if newKey == "" && !dryRun {
				return errors.New("provide --new-key, or --dry-run to validate the current key")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			oldBox, err := auth.NewSecretBoxFromMasterKey(cfg.Auth.MasterEncryptionKey)
			if err != nil {
				return fmt.Errorf("build old secretbox: %w", err)
			}
			newBox := oldBox // dry-run: encrypt step is never taken; new==old is fine
			if newKey != "" {
				newBox, err = auth.NewSecretBoxFromMasterKey(newKey)
				if err != nil {
					return fmt.Errorf("build new secretbox: %w", err)
				}
			}

			ctx := cmd.Context()
			pool, err := db.Open(ctx, db.Config{
				URL:                      cfg.Database.URL,
				MaxConns:                 cfg.Database.MaxConns,
				MinConns:                 cfg.Database.MinConns,
				StatementTimeout:         cfg.Database.StatementTimeout,
				LockTimeout:              cfg.Database.LockTimeout,
				IdleInTransactionTimeout: cfg.Database.IdleInTransactionTimeout,
				ApplicationName:          "cairn-rotate-key",
			})
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer pool.Close()

			r := &rotator{pool: pool, oldBox: oldBox, newBox: newBox, dryRun: dryRun}
			if dryRun {
				fmt.Println("rotate-key: DRY RUN — validating the current key decrypts every secret (no writes)")
			} else {
				fmt.Println("rotate-key: re-encrypting all secrets-at-rest under the new key")
			}

			steps := []struct {
				name string
				fn   func(context.Context) error
			}{
				{"external_accounts (oauth tokens)", r.rotateOAuthTokens},
				{"user_provider_configs (client secrets)", r.rotateUserProviderConfigs},
				{"instance_signing_keys (account seed)", r.rotateSigningKeys},
			}
			for _, s := range steps {
				if err := s.fn(ctx); err != nil {
					return fmt.Errorf("%s: %w", s.name, err)
				}
			}

			r.report()
			if r.failed > 0 && !skipUnreadable {
				return fmt.Errorf("%d ciphertext(s) could not be decrypted under the old OR new key — see above; "+
					"re-enter those secrets, or pass --skip-unreadable to rotate the rest", r.failed)
			}
			if r.failed > 0 {
				fmt.Printf("rotate-key: WARNING — %d unreadable ciphertext(s) left untouched (still under the old key); "+
					"re-enter those secrets after rotating the master key.\n", r.failed)
			}
			if dryRun {
				fmt.Println("rotate-key: dry run OK — the current key decrypts every secret.")
			} else {
				fmt.Println("rotate-key: done. Set CAIRN_AUTH_MASTER_ENCRYPTION_KEY to the new key and restart the server + workers.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&newKey, "new-key", "", "new master key (base64-encoded 32 bytes, or a passphrase)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate the current key decrypts all secrets; make no changes")
	cmd.Flags().BoolVar(&skipUnreadable, "skip-unreadable", false, "rotate readable secrets even if some can't be decrypted (those are left under the old key)")
	return cmd
}

type rotator struct {
	pool   *pgxpool.Pool
	oldBox *auth.SecretBox
	newBox *auth.SecretBox
	dryRun bool

	rotated int // re-encrypted (or, in dry-run, would have been)
	already int // already encrypted under the new key (resumable re-run)
	empty   int // null/empty ciphertext, nothing to do
	failed  int // could not decrypt under old OR new key
}

// reencrypt decrypts ct under the old key and returns the new-key ciphertext.
// status is one of: "empty" (nil ct), "rotated", "already" (decrypts under the
// new key — resumable), "fail" (neither key works). On "rotated" the caller
// writes newCt back; other statuses require no write.
func (r *rotator) reencrypt(ct, aad []byte) (newCt []byte, status string) {
	if len(ct) == 0 {
		return nil, "empty"
	}
	pt, err := r.oldBox.Decrypt(ct, aad)
	if err != nil {
		// Resumable: maybe this row was already re-encrypted under the new key.
		if _, e2 := r.newBox.Decrypt(ct, aad); e2 == nil {
			return nil, "already"
		}
		return nil, "fail"
	}
	if r.dryRun {
		return nil, "rotated" // counted, but never written in dry-run
	}
	out, err := r.newBox.Encrypt(pt, aad)
	if err != nil {
		return nil, "fail"
	}
	return out, "rotated"
}

// tally records a status and reports per-row failures.
func (r *rotator) tally(status, table, id string) {
	switch status {
	case "rotated":
		r.rotated++
	case "already":
		r.already++
	case "empty":
		r.empty++
	case "fail":
		r.failed++
		fmt.Printf("  FAIL  %s/%s: ciphertext decrypts under neither the old nor the new key\n", table, id)
	}
}

func (r *rotator) update(ctx context.Context, sql string, args ...any) error {
	if r.dryRun {
		return nil
	}
	_, err := r.pool.Exec(ctx, sql, args...)
	return err
}

func (r *rotator) rotateOAuthTokens(ctx context.Context) error {
	rows, err := r.pool.Query(ctx,
		`SELECT id, access_token_encrypted, refresh_token_encrypted FROM external_accounts`)
	if err != nil {
		return err
	}
	type row struct {
		id          string
		access, ref []byte
	}
	var batch []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.access, &rr.ref); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, rr := range batch {
		aad := []byte("oauth_token:" + rr.id)
		newAccess, sA := r.reencrypt(rr.access, aad)
		newRef, sR := r.reencrypt(rr.ref, aad)
		r.tally(sA, "external_accounts.access", rr.id)
		r.tally(sR, "external_accounts.refresh", rr.id)
		if sA == "rotated" || sR == "rotated" {
			ca := rr.access
			if sA == "rotated" {
				ca = newAccess
			}
			cr := rr.ref
			if sR == "rotated" {
				cr = newRef
			}
			if err := r.update(ctx,
				`UPDATE external_accounts SET access_token_encrypted=$1, refresh_token_encrypted=$2 WHERE id=$3`,
				ca, cr, rr.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *rotator) rotateUserProviderConfigs(ctx context.Context) error {
	rows, err := r.pool.Query(ctx,
		`SELECT id, client_secret_encrypted FROM user_provider_configs`)
	if err != nil {
		return err
	}
	type row struct {
		id string
		ct []byte
	}
	var batch []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.ct); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, rr := range batch {
		aad := []byte("user_provider_config:" + rr.id)
		newCt, status := r.reencrypt(rr.ct, aad)
		r.tally(status, "user_provider_configs", rr.id)
		if status == "rotated" {
			if err := r.update(ctx,
				`UPDATE user_provider_configs SET client_secret_encrypted=$1 WHERE id=$2`, newCt, rr.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *rotator) rotateSigningKeys(ctx context.Context) error {
	rows, err := r.pool.Query(ctx,
		`SELECT id, purpose, seed_encrypted FROM instance_signing_keys`)
	if err != nil {
		return err
	}
	type row struct {
		id, purpose string
		ct          []byte
	}
	var batch []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.purpose, &rr.ct); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, rr := range batch {
		aad := []byte("signing_key:" + rr.purpose)
		newCt, status := r.reencrypt(rr.ct, aad)
		r.tally(status, "instance_signing_keys", rr.id)
		if status == "rotated" {
			if err := r.update(ctx,
				`UPDATE instance_signing_keys SET seed_encrypted=$1 WHERE id=$2`, newCt, rr.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *rotator) report() {
	verb := "re-encrypted"
	if r.dryRun {
		verb = "would re-encrypt"
	}
	fmt.Printf("rotate-key summary: %s %d, already-new %d, empty %d, failed %d\n",
		verb, r.rotated, r.already, r.empty, r.failed)
}
