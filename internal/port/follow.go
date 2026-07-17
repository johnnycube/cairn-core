package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FollowRepo persists the social follow graph (migration 32).
type FollowRepo interface {
	// Follow creates an accepted edge follower→followee (idempotent).
	Follow(ctx context.Context, follower, followee domain.UserID) error
	// Unfollow removes the edge (idempotent).
	Unfollow(ctx context.Context, follower, followee domain.UserID) error
	// IsFollowing reports whether follower has an accepted edge to followee.
	IsFollowing(ctx context.Context, follower, followee domain.UserID) (bool, error)
	// ListFollowers / ListFollowing return user IDs (accepted edges).
	ListFollowers(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.UserID, error)
	ListFollowing(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.UserID, error)
	Counts(ctx context.Context, userID domain.UserID) (domain.FollowCounts, error)
}
