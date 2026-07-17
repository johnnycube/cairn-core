package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// PasskeyRepo persists WebAuthn credentials. The repo stores the credential
// material opaquely (domain.Passkey.CredentialJSON); the WebAuthn ceremony
// logic lives in the primary adapter.
type PasskeyRepo interface {
	// Create stores a newly-registered credential.
	Create(ctx context.Context, p domain.Passkey) (domain.PasskeyID, error)

	// ListByUser returns all of a user's passkeys (newest first).
	ListByUser(ctx context.Context, userID domain.UserID) ([]domain.Passkey, error)

	// GetByCredentialID looks up a passkey by its raw WebAuthn credential id.
	// Returns domain.ErrNotFound when absent. Used by the login ceremony.
	GetByCredentialID(ctx context.Context, credentialID []byte) (domain.Passkey, error)

	// UpdateCredential replaces the stored credential blob (e.g. after the sign
	// counter advances on login) and stamps last_used_at.
	UpdateCredential(ctx context.Context, credentialID []byte, credentialJSON []byte) error

	// Rename sets a passkey's label (ownership-scoped).
	Rename(ctx context.Context, userID domain.UserID, id domain.PasskeyID, name string) error

	// Delete removes a passkey (ownership-scoped).
	Delete(ctx context.Context, userID domain.UserID, id domain.PasskeyID) error
}
