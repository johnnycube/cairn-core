package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ClubRepo persists clubs + memberships (migration 38).
type ClubRepo interface {
	Create(ctx context.Context, c domain.Club) (domain.ClubID, error)
	GetBySlug(ctx context.Context, slug string) (domain.Club, error)
	// List returns public clubs plus any private clubs the viewer belongs to.
	List(ctx context.Context, viewer domain.UserID, limit, offset int) ([]domain.Club, error)
	ListForUser(ctx context.Context, userID domain.UserID) ([]domain.Club, error)

	AddMember(ctx context.Context, clubID domain.ClubID, userID domain.UserID, role domain.ClubMemberRole) error
	RemoveMember(ctx context.Context, clubID domain.ClubID, userID domain.UserID) error
	ListMembers(ctx context.Context, clubID domain.ClubID, limit, offset int) ([]domain.ClubMember, error)
	MemberRole(ctx context.Context, clubID domain.ClubID, userID domain.UserID) (domain.ClubMemberRole, bool, error)
	CountMembers(ctx context.Context, clubID domain.ClubID) (int, error)

	// ListClubFeed returns recent non-private activities of the club's members.
	ListClubFeed(ctx context.Context, clubID domain.ClubID, limit, offset int) ([]domain.Activity, error)
}
