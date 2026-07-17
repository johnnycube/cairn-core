package domain

import "time"

// Passkey is a registered WebAuthn credential (an authenticator the user can
// sign in with). The cryptographic material — public key, sign counter, AAGUID,
// transports — is stored opaquely in CredentialJSON (the go-webauthn Credential,
// serialised). The domain/port layers deliberately don't depend on the WebAuthn
// library type; the primary adapter that runs the ceremony marshals to/from it.
type Passkey struct {
	ID     PasskeyID
	UserID UserID

	// CredentialID is the raw WebAuthn credential id — the lookup key for the
	// login ceremony (an authenticator returns it in the assertion).
	CredentialID []byte

	// CredentialJSON is the opaque, serialised go-webauthn Credential.
	CredentialJSON []byte

	// Name is a user-facing label ("MacBook Touch ID", "YubiKey 5").
	Name string

	CreatedAt  time.Time
	LastUsedAt *time.Time
}
