package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// PATRepo persists personal access tokens (hashes only).
type PATRepo interface {
	// Create stores a new token (code already hashed by the caller).
	Create(ctx context.Context, p domain.PersonalAccessToken, tokenHash []byte) (domain.PATID, error)

	// FindByTokenHash resolves a token for authentication. Returns
	// domain.ErrNotFound when absent.
	FindByTokenHash(ctx context.Context, tokenHash []byte) (domain.PersonalAccessToken, error)

	// ListForUser returns a user's tokens (newest first); hashes are never returned.
	ListForUser(ctx context.Context, userID domain.UserID) ([]domain.PersonalAccessToken, error)

	// TouchLastUsed stamps last_used_at (best-effort, on each authenticated call).
	TouchLastUsed(ctx context.Context, id domain.PATID) error

	// Revoke marks a token revoked (ownership-scoped).
	Revoke(ctx context.Context, userID domain.UserID, id domain.PATID) error
}
