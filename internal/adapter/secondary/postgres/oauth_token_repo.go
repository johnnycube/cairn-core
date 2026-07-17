package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// OAuthTokenRepo implements port.OAuthTokenRepo using the access_token_encrypted
// and refresh_token_encrypted columns of external_accounts (migration 4).
//
// All ciphertext is bound to the account via an associated-data tag
// ("oauth_token:<account_id>") so an attacker with DB access can't
// swap two accounts' ciphertexts and have them decrypt anywhere.
type OAuthTokenRepo struct {
	pool    *pgxpool.Pool
	secrets *auth.SecretBox
}

func NewOAuthTokenRepo(pool *pgxpool.Pool, secrets *auth.SecretBox) *OAuthTokenRepo {
	return &OAuthTokenRepo{pool: pool, secrets: secrets}
}

// GetToken returns the decrypted token state. Returns ErrTokenAccountNotFound
// when the account is unknown OR is in needs_reauth status.
func (r *OAuthTokenRepo) GetToken(
	ctx context.Context,
	accountID domain.ExternalAccountID,
) (port.TokenState, error) {
	db := dbtx(ctx, r.pool)

	var (
		accessEnc  []byte
		refreshEnc []byte
		expiresAt  *time.Time
		scopes     []string
		status     string
	)
	err := db.QueryRow(ctx,
		`SELECT access_token_encrypted, refresh_token_encrypted,
		        access_token_expires_at, granted_scopes, status
		   FROM external_accounts
		  WHERE id = $1`,
		accountID.UUID(),
	).Scan(&accessEnc, &refreshEnc, &expiresAt, &scopes, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.TokenState{}, port.ErrTokenAccountNotFound
	}
	if err != nil {
		return port.TokenState{}, fmt.Errorf("get oauth token %s: %w", accountID, err)
	}

	if status == "needs_reauth" || status == "revoked" {
		return port.TokenState{}, port.ErrTokenAccountNotFound
	}
	if len(accessEnc) == 0 {
		// No token stored yet — initial OAuth not completed.
		return port.TokenState{}, port.ErrTokenAccountNotFound
	}

	aad := []byte("oauth_token:" + accountID.String())
	accessPlain, err := r.secrets.Decrypt(accessEnc, aad)
	if err != nil {
		return port.TokenState{}, fmt.Errorf("decrypt access token: %w", err)
	}
	defer wipe(accessPlain)

	var refreshPlain []byte
	if len(refreshEnc) > 0 {
		refreshPlain, err = r.secrets.Decrypt(refreshEnc, aad)
		if err != nil {
			return port.TokenState{}, fmt.Errorf("decrypt refresh token: %w", err)
		}
		defer wipe(refreshPlain)
	}

	st := port.TokenState{
		AccessToken:  string(accessPlain),
		RefreshToken: string(refreshPlain),
		TokenType:    "Bearer", // not stored; convention for the providers we support
	}
	if expiresAt != nil {
		st.ExpiresAt = *expiresAt
	}
	if len(scopes) > 0 {
		st.Scope = joinScopes(scopes)
	}
	return st, nil
}

// StoreToken writes the encrypted token state with optimistic concurrency.
//
// Concurrency contract: the write only takes effect if the current
// on-disk token_expires_at is <= PreviousExpiresAt. This catches the
// case where two workers refresh the same token in parallel:
//
//	Worker A refresh starts at T1 with expiry E0
//	Worker B refresh starts at T2 > T1 with expiry E0
//	Worker A writes expiry E1 at T3
//	Worker B writes expiry E2 at T4 > T3
//	  → B sees on-disk expiry E1 > B's PreviousExpiresAt E0 → reject
//	  → B re-fetches → gets A's state → no work duplicated
//
// PreviousExpiresAt = zero (no prior state) is allowed for the initial
// OAuth callback path.
func (r *OAuthTokenRepo) StoreToken(
	ctx context.Context,
	accountID domain.ExternalAccountID,
	in port.StoreTokenInput,
) error {
	db := dbtx(ctx, r.pool)
	aad := []byte("oauth_token:" + accountID.String())

	accessEnc, err := r.secrets.Encrypt([]byte(in.State.AccessToken), aad)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	var refreshEnc []byte
	if in.State.RefreshToken != "" {
		refreshEnc, err = r.secrets.Encrypt([]byte(in.State.RefreshToken), aad)
		if err != nil {
			return fmt.Errorf("encrypt refresh token: %w", err)
		}
	}

	// CAS: only write if current on-disk expires_at is <= our previous.
	// `IS NULL` arm covers the initial-store case.
	tag, err := db.Exec(ctx,
		`UPDATE external_accounts
		    SET access_token_encrypted   = $2,
		        refresh_token_encrypted  = COALESCE($3, refresh_token_encrypted),
		        access_token_expires_at  = $4,
		        granted_scopes           = CASE WHEN $5::text[] IS NOT NULL THEN $5::text[] ELSE granted_scopes END,
		        updated_at               = now()
		  WHERE id = $1
		    AND (
		         access_token_expires_at IS NULL
		         OR access_token_expires_at <= $6
		    )`,
		accountID.UUID(),
		accessEnc,
		nullableBytes(refreshEnc),
		in.State.ExpiresAt,
		scopeArrayOrNil(in.State.Scope),
		casPreviousTime(in.PreviousExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("store oauth token %s: %w", accountID, err)
	}
	if tag.RowsAffected() == 0 {
		// Either the account doesn't exist (rare race with disconnect)
		// or the CAS failed (another worker won).
		var exists bool
		if err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM external_accounts WHERE id = $1)`,
			accountID.UUID(),
		).Scan(&exists); err != nil {
			return fmt.Errorf("verify account exists: %w", err)
		}
		if !exists {
			return port.ErrTokenAccountNotFound
		}
		return port.ErrTokenStaleStore
	}
	return nil
}

// MarkNeedsReauth flips status='needs_reauth' and stamps the reason.
// The caller (usecase layer) publishes the needs_reauth event; this
// adapter is just the DB write.
func (r *OAuthTokenRepo) MarkNeedsReauth(
	ctx context.Context,
	accountID domain.ExternalAccountID,
	reason string,
) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`UPDATE external_accounts
		    SET status         = 'needs_reauth',
		        status_reason  = $2,
		        updated_at     = now()
		  WHERE id = $1
		    AND status != 'needs_reauth'`,
		accountID.UUID(), reason,
	)
	if err != nil {
		return fmt.Errorf("mark needs_reauth %s: %w", accountID, err)
	}
	if tag.RowsAffected() == 0 {
		// Either already-marked (idempotent no-op) or missing.
		var exists bool
		_ = db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM external_accounts WHERE id = $1)`,
			accountID.UUID(),
		).Scan(&exists)
		if !exists {
			return port.ErrTokenAccountNotFound
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func casPreviousTime(t time.Time) any {
	if t.IsZero() {
		// "Never previously had a token" — match the IS NULL arm.
		return time.Time{}.Add(1) // a sentinel that's still <= any real value
	}
	return t
}

// scopeArrayOrNil parses a "read,activity:read_all"-style scope string
// into the text[] form Postgres stores. Empty input → nil (no-op
// COALESCE in SQL leaves existing scopes alone).
func scopeArrayOrNil(s string) any {
	if s == "" {
		return nil
	}
	parts := splitComma(s)
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func joinScopes(scopes []string) string {
	out := ""
	for i, s := range scopes {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// wipe zeroes a byte slice. Best-effort defense against plaintext key
// material lingering in process memory.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
