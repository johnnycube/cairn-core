package domain

import (
	"time"

	"github.com/google/uuid"
)

// ConnectionID identifies one connection (a user's configured link to a
// provider, with its own OAuth-app credentials).
type ConnectionID uuid.UUID

func (id ConnectionID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id ConnectionID) String() string  { return uuid.UUID(id).String() }

// UserProviderConfig is ONE connection: a user's own credentials/config for a
// provider (e.g. a Strava API app). A user may have MANY connections per
// provider — each a fully separate connection with its OWN client_id/secret
// (e.g. two different Strava apps / accounts). The OAuth account a connection
// produces links back to it via external_accounts.connection_id.
//
// ClientSecret is plaintext at the port boundary; the adapter encrypts it at
// rest. On list views ClientSecret is blank and HasSecret indicates presence.
type UserProviderConfig struct {
	ID           ConnectionID
	UserID       UserID
	Provider     string
	Label        string
	ClientID     string
	ClientSecret string
	HasSecret    bool
	// SecretUnreadable is true when a secret is stored but could not be
	// decrypted (e.g. after an AAD/key change). The connection is still
	// usable for management (rename, re-enter secret); only token exchange
	// is blocked until the secret is re-saved.
	SecretUnreadable bool
	Config           map[string]string

	CreatedAt time.Time
	UpdatedAt time.Time
}
