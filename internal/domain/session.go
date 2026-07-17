package domain

import (
	"net/netip"
	"time"
)

// ---------------------------------------------------------------------------
// Session
//
// Server-side persistent session record. The plaintext bearer token (or
// session cookie value) lives only in the client; the DB stores its
// SHA-256 in token_hash. Lookups are by hash so even DB-read disclosure
// doesn't allow session impersonation.
//
// Auth methods mirror migration 00002's CHECK constraint.
// ---------------------------------------------------------------------------

type Session struct {
	ID         SessionID
	UserID     UserID
	TokenHash  []byte // 32 bytes (SHA-256 of plaintext)
	AuthMethod SessionAuthMethod

	UserAgentSummary string
	IPAddress        netip.Addr
	IPGeoSummary     string

	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

// IsActive reports whether the session is still usable: not revoked and
// not past its expiry. Callers MUST check this — the repo does not
// filter expired/revoked rows automatically.
func (s *Session) IsActive(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	return now.Before(s.ExpiresAt)
}

// SessionAuthMethod enumerates the value space of migration 00002's
// CHECK constraint on sessions.auth_method.
type SessionAuthMethod string

const (
	SessionAuthPassword             SessionAuthMethod = "password"
	SessionAuthPasswordWithWebauthn SessionAuthMethod = "password_with_webauthn_2fa"
	SessionAuthPasskey              SessionAuthMethod = "webauthn_passkey"
	SessionAuthOIDC                 SessionAuthMethod = "oidc"
	SessionAuthPAT                  SessionAuthMethod = "personal_access_token"
)

func (m SessionAuthMethod) Valid() bool {
	switch m {
	case SessionAuthPassword, SessionAuthPasswordWithWebauthn,
		SessionAuthPasskey, SessionAuthOIDC, SessionAuthPAT:
		return true
	}
	return false
}
