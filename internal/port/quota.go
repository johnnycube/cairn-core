package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// QuotaRepo persists per-user quota overrides (migration 37). A nil override
// means "use the instance default".
type QuotaRepo interface {
	// GetMaxActivities returns the per-user override (nil = use instance
	// default). A non-nil 0 means "unlimited for this user".
	GetMaxActivities(ctx context.Context, userID domain.UserID) (*int, error)
	SetMaxActivities(ctx context.Context, userID domain.UserID, max *int) error
}
