package domain

import (
	"errors"
	"time"
)

// Invite is a single-use signup token an admin issues so a new user can
// self-register on an invite-only instance. The plaintext code is shown once
// at creation; only its hash is stored. Redeeming creates a user with the
// invite's assigned role and (optionally) pins the email.
type Invite struct {
	ID            InviteID
	CodePrefix    string // human preview for the admin list ("abc123…")
	Email         string // optional: pins the invited address when set
	AssignedRole  UserRole
	CreatedByUser *UserID
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	UsedAt        *time.Time
	UsedByUser    *UserID
	RevokedAt     *time.Time
}

// Sentinel errors for the invite flow.
var (
	ErrInviteNotFound = errors.New("invite: not found")
	ErrInviteInvalid  = errors.New("invite: invalid, already used, expired, or revoked")
)

// IsRedeemableAt reports whether the invite can be redeemed at `now` — not
// used, not revoked, not past expiry.
func (i Invite) IsRedeemableAt(now time.Time) bool {
	if i.UsedAt != nil || i.RevokedAt != nil {
		return false
	}
	if i.ExpiresAt != nil && !now.Before(*i.ExpiresAt) {
		return false
	}
	return true
}

// Status is a UI-friendly state string.
func (i Invite) Status(now time.Time) string {
	switch {
	case i.RevokedAt != nil:
		return "revoked"
	case i.UsedAt != nil:
		return "used"
	case i.ExpiresAt != nil && !now.Before(*i.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}
