package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// UserProviderConfigRepo persists connections — a user's own provider OAuth-app
// credentials. A user may have MANY connections per provider, each identified by
// its ConnectionID. Secrets are encrypted at rest by the adapter.
type UserProviderConfigRepo interface {
	// Create inserts a new connection and returns its id.
	Create(ctx context.Context, cfg domain.UserProviderConfig) (domain.ConnectionID, error)

	// Update modifies a connection by id (label / client_id / client_secret /
	// config). An empty ClientSecret leaves the stored secret untouched.
	Update(ctx context.Context, cfg domain.UserProviderConfig) error

	// GetByID returns one connection (with DECRYPTED ClientSecret) by id.
	// Returns domain.ErrNotFound when missing.
	GetByID(ctx context.Context, id domain.ConnectionID) (domain.UserProviderConfig, error)

	// ListForUser returns the user's connections WITHOUT secrets (ClientSecret
	// blank, HasSecret set) — for the Connections UI.
	ListForUser(ctx context.Context, userID domain.UserID) ([]domain.UserProviderConfig, error)

	// Delete removes a connection by id.
	Delete(ctx context.Context, id domain.ConnectionID) error
}
