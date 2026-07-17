package domain

import (
	"time"

	"github.com/google/uuid"
)

// Clubs / groups / teams (multi-user v1).

// ClubID identifies a club.
type ClubID uuid.UUID

func (id ClubID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id ClubID) String() string  { return uuid.UUID(id).String() }

// ClubMemberRole is a member's role within a club.
type ClubMemberRole string

const (
	ClubRoleOwner  ClubMemberRole = "owner"
	ClubRoleAdmin  ClubMemberRole = "admin"
	ClubRoleMember ClubMemberRole = "member"
)

// Club is a named group of athletes.
type Club struct {
	ID          ClubID
	Slug        string
	Name        string
	Description string
	OwnerID     UserID
	IsPublic    bool
	CreatedAt   time.Time
}

// ClubMember is a user's membership in a club.
type ClubMember struct {
	ClubID   ClubID
	UserID   UserID
	Role     ClubMemberRole
	JoinedAt time.Time
}

// MaxClubNameLength / MaxClubDescriptionLength bound club text.
const (
	MaxClubNameLength        = 100
	MaxClubDescriptionLength = 2000
)
