// Package invite implements the signup-invite flow: an admin mints a single-use
// code; a prospective user redeems it to self-register on an invite-only
// instance. Codes are returned in plaintext exactly once and stored only as a
// SHA-256 hash.
package invite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// CreateInvite mints invites.
type CreateInvite struct {
	invites port.InviteRepo
	now     func() time.Time
}

func NewCreateInvite(invites port.InviteRepo, now func() time.Time) *CreateInvite {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &CreateInvite{invites: invites, now: now}
}

// CreateInviteInput configures a new invite.
type CreateInviteInput struct {
	Role      domain.UserRole // defaults to user
	Email     string          // optional: pins the invited address
	ExpiresIn time.Duration   // 0 = never expires
	CreatedBy *domain.UserID
}

// CreateInviteResult carries the one-time plaintext code.
type CreateInviteResult struct {
	ID        domain.InviteID
	Code      string // SHOW ONCE — never stored
	Prefix    string
	ExpiresAt *time.Time
}

func (uc *CreateInvite) Execute(ctx context.Context, in CreateInviteInput) (CreateInviteResult, error) {
	role := in.Role
	if role == "" {
		role = domain.UserRoleUser
	}
	if !role.Valid() {
		return CreateInviteResult{}, domain.ErrInvalidUserRole
	}

	// 24 random bytes → URL-safe code.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return CreateInviteResult{}, fmt.Errorf("generate invite code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(code))
	prefix := code[:8] + "…"

	var expiresAt *time.Time
	if in.ExpiresIn > 0 {
		t := uc.now().Add(in.ExpiresIn)
		expiresAt = &t
	}

	inv := domain.Invite{
		CodePrefix:    prefix,
		Email:         in.Email,
		AssignedRole:  role,
		CreatedByUser: in.CreatedBy,
		ExpiresAt:     expiresAt,
	}
	id, err := uc.invites.Create(ctx, inv, sum[:])
	if err != nil {
		return CreateInviteResult{}, err
	}
	return CreateInviteResult{ID: id, Code: code, Prefix: prefix, ExpiresAt: expiresAt}, nil
}
