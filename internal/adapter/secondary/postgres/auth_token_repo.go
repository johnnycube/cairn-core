package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// AuthTokenRepo implements port.AuthTokenRepo over the password_reset_tokens and
// email_verification_tokens tables (migration 2). Tokens are stored as the raw
// sha256 hash (bytea PK); the plaintext is shown once in the email link.
type AuthTokenRepo struct {
	pool *pgxpool.Pool
}

func NewAuthTokenRepo(pool *pgxpool.Pool) *AuthTokenRepo {
	return &AuthTokenRepo{pool: pool}
}

func (r *AuthTokenRepo) CreatePasswordResetToken(
	ctx context.Context, userID domain.UserID, tokenHash []byte, expiresAt time.Time,
) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID.UUID(), tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}
	return nil
}

func (r *AuthTokenRepo) ConsumePasswordResetToken(
	ctx context.Context, tokenHash []byte, now time.Time,
) (domain.UserID, error) {
	db := dbtx(ctx, r.pool)
	var uid uuid.UUID
	err := db.QueryRow(ctx,
		`UPDATE password_reset_tokens
		    SET used_at = $2
		  WHERE token_hash = $1
		    AND used_at IS NULL
		    AND expires_at > $2
		 RETURNING user_id`,
		tokenHash, now,
	).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserID{}, port.ErrAuthTokenInvalid
	}
	if err != nil {
		return domain.UserID{}, fmt.Errorf("consume password reset token: %w", err)
	}
	return domain.UserID(uid), nil
}

func (r *AuthTokenRepo) CreateEmailVerificationToken(
	ctx context.Context, userID domain.UserID, email string, tokenHash []byte, expiresAt time.Time,
) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO email_verification_tokens (user_id, token_hash, email, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		userID.UUID(), tokenHash, email, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create email verification token: %w", err)
	}
	return nil
}

func (r *AuthTokenRepo) ConsumeEmailVerificationToken(
	ctx context.Context, tokenHash []byte, now time.Time,
) (domain.UserID, string, error) {
	db := dbtx(ctx, r.pool)
	var (
		uid   uuid.UUID
		email string
	)
	err := db.QueryRow(ctx,
		`UPDATE email_verification_tokens
		    SET used_at = $2
		  WHERE token_hash = $1
		    AND used_at IS NULL
		    AND expires_at > $2
		 RETURNING user_id, email`,
		tokenHash, now,
	).Scan(&uid, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserID{}, "", port.ErrAuthTokenInvalid
	}
	if err != nil {
		return domain.UserID{}, "", fmt.Errorf("consume email verification token: %w", err)
	}
	return domain.UserID(uid), email, nil
}
