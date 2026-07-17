package domain

import "time"

// PersonalAccessToken is a long-lived bearer credential a user mints for the
// CLI / API clients. Only the SHA-256 hash is stored; the plaintext is shown
// once at creation. A PAT authenticates as its owning user (scopes are stored
// for future granular enforcement; today a PAT carries the user's full access).
type PersonalAccessToken struct {
	ID          PATID
	UserID      UserID
	Name        string
	TokenPrefix string // first chars of the plaintext, for identify-without-exposing
	Scopes      []string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
}

// IsValidAt reports whether the token can authenticate at `now` (not revoked,
// not expired).
func (p PersonalAccessToken) IsValidAt(now time.Time) bool {
	if p.RevokedAt != nil {
		return false
	}
	if p.ExpiresAt != nil && !now.Before(*p.ExpiresAt) {
		return false
	}
	return true
}
