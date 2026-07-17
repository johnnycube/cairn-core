package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// VisibilityRepo persists the per-field visibility model (migration 31):
// per-user default policy, per-activity override, and privacy zones.
type VisibilityRepo interface {
	// GetUserPolicy returns the user's default visibility policy. An empty
	// policy (no row) means "use domain.DefaultVisibilityPolicy".
	GetUserPolicy(ctx context.Context, userID domain.UserID) (domain.VisibilityPolicy, error)
	SetUserPolicy(ctx context.Context, userID domain.UserID, p domain.VisibilityPolicy) error

	// GetActivityOverride returns the per-activity override (nil when none).
	GetActivityOverride(ctx context.Context, activityID domain.ActivityID) (*domain.VisibilityPolicy, error)
	SetActivityOverride(ctx context.Context, activityID domain.ActivityID, p *domain.VisibilityPolicy) error

	ListPrivacyZones(ctx context.Context, userID domain.UserID) ([]domain.PrivacyZone, error)
	AddPrivacyZone(ctx context.Context, z domain.PrivacyZone) (domain.PrivacyZoneID, error)
	DeletePrivacyZone(ctx context.Context, id domain.PrivacyZoneID, userID domain.UserID) error
}
