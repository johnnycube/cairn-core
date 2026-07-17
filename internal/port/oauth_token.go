package port

import (
	"context"
	"errors"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ---------------------------------------------------------------------------
// OAuthTokenRepo
//
// Provider-agnostic OAuth token store. The server has no idea how Strava
// vs Garmin vs Polar handle their refresh flows — it just stores what
// the worker tells it to.
//
// Threat model:
//
//   - Server is the encryption authority. The adapter holds the
//     SecretBox; ciphertext lives in external_accounts.access_token_encrypted
//     and refresh_token_encrypted (from migration 4).
//   - Workers receive plaintext tokens over NATS (which is TLS-protected
//     in production). They hold the tokens in-process only — restart
//     re-fetches from the server.
//   - Refresh tokens flow over the same channel as access tokens. This
//     is a deliberate trade-off: the alternative is having the server
//     hold provider-specific refresh code, which couples the core to
//     every provider. Refresh tokens are bearer credentials; if a worker
//     is compromised, it can already exfiltrate access tokens used in
//     in-flight API calls. The threat model is "trust the worker fleet";
//     enforcement happens via NATS Account isolation (see architecture.md
//     §4).
//
// Optimistic concurrency:
//
//   - StoreToken takes the previous ExpiresAt; the adapter rejects the
//     write if the on-disk row has a newer expiry. This prevents lost
//     updates when two worker instances refresh the same account in
//     parallel (rare but possible during webhook bursts).
//   - On rejection, the calling worker re-fetches via GetToken and
//     uses whatever the winning worker stored.
// ---------------------------------------------------------------------------

// ErrTokenStaleStore signals that the worker tried to StoreToken with
// data older than what's already on disk. Caller should re-fetch.
var ErrTokenStaleStore = errors.New("oauth_token: stale store; refetch")

// ErrTokenAccountNotFound is returned by GetToken when the account
// doesn't exist or doesn't have stored tokens (initial OAuth never
// completed). Maps to "account_gone" in the worker SDK.
var ErrTokenAccountNotFound = errors.New("oauth_token: account not found or no token")

type OAuthTokenRepo interface {
	// GetToken returns the plaintext token state for the account.
	// Adapter decrypts before returning.
	//
	// Returns ErrTokenAccountNotFound when the account is unknown OR
	// when status='needs_reauth' (in which case the worker should not
	// retry the underlying job; the SDK Term()s on this path).
	GetToken(ctx context.Context, accountID domain.ExternalAccountID) (TokenState, error)

	// StoreToken atomically replaces the encrypted token state. The
	// PreviousExpiresAt is used for optimistic concurrency: writes
	// where the on-disk expires_at is >= PreviousExpiresAt + 1s are
	// rejected with ErrTokenStaleStore.
	//
	// PreviousExpiresAt = zero is allowed for the initial OAuth-callback
	// path where no prior state exists.
	StoreToken(ctx context.Context, accountID domain.ExternalAccountID, in StoreTokenInput) error

	// MarkNeedsReauth flips status='needs_reauth' and records a reason.
	// Called by the worker when the provider returns invalid_grant or
	// any other permanent refresh failure.
	//
	// Emits cairn.events.external_account.needs_reauth so the user-facing
	// notification dispatcher can alert the user.
	MarkNeedsReauth(ctx context.Context, accountID domain.ExternalAccountID, reason string) error
}

// TokenState is the unencrypted token bundle that crosses the port.
// Workers see this over NATS request/reply; the adapter persists it
// encrypted.
type TokenState struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	TokenType    string // typically "Bearer"
}

// StoreTokenInput is what workers send when they've successfully
// refreshed (or initially obtained) a token.
type StoreTokenInput struct {
	State             TokenState
	PreviousExpiresAt time.Time // for optimistic concurrency
}
