package port

import (
	"context"
	"errors"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ErrAuthTokenInvalid is returned by the Consume* methods when a token does not
// exist, was already used, or has expired. The handler maps all three to the
// same generic "invalid or expired link" response so it leaks nothing.
var ErrAuthTokenInvalid = errors.New("auth token invalid, used, or expired")

// AuthTokenRepo persists the two one-time email-token flows on the
// password_reset_tokens and email_verification_tokens tables (migration 2).
// Only the sha256 of the plaintext token is stored; Consume* is an atomic
// single-use claim (UPDATE … SET used_at WHERE redeemable RETURNING).
type AuthTokenRepo interface {
	CreatePasswordResetToken(ctx context.Context, userID domain.UserID, tokenHash []byte, expiresAt time.Time) error
	// ConsumePasswordResetToken atomically marks a valid token used and returns
	// its user. Returns ErrAuthTokenInvalid if not redeemable.
	ConsumePasswordResetToken(ctx context.Context, tokenHash []byte, now time.Time) (domain.UserID, error)

	CreateEmailVerificationToken(ctx context.Context, userID domain.UserID, email string, tokenHash []byte, expiresAt time.Time) error
	// ConsumeEmailVerificationToken atomically marks a valid token used and
	// returns its user + the email it was issued for.
	ConsumeEmailVerificationToken(ctx context.Context, tokenHash []byte, now time.Time) (userID domain.UserID, email string, err error)
}
