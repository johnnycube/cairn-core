package port

import (
	"context"

	"github.com/opencairn-org/cairn-core/internal/domain"
)

// GearRepo persists gear, its provider refs and the user's per-type defaults,
// and maintains the derived totals. Gear-attachment queries over activities
// live here too (repoint on merge, totals aggregate) so they stay next to
// the invariants they serve.
type GearRepo interface {
	// Get returns one gear with its external refs. ErrNotFound when absent.
	Get(ctx context.Context, id domain.GearID) (domain.Gear, error)
	// ListForUser returns the user's gear (with refs), active first, then by
	// name. Retired gear is included only when includeRetired is set.
	ListForUser(ctx context.Context, userID domain.UserID, includeRetired bool) ([]domain.Gear, error)
	// Save upserts the gear row by ID. Totals and refs are not written here.
	Save(ctx context.Context, g domain.Gear) error
	// Delete removes the gear; activities keep no gear (FK SET NULL), refs
	// and defaults cascade. ErrNotFound when absent.
	Delete(ctx context.Context, id domain.GearID) error

	// UpsertExternalRef creates or refreshes a provider ref. The ref's key is
	// unique: an upsert for an existing key updates its gear_id and fields.
	UpsertExternalRef(ctx context.Context, ref domain.GearExternalRef) error
	// FindByExternalRef returns the gear id a provider ref resolves to.
	FindByExternalRef(ctx context.Context, key domain.GearRefKey) (domain.GearID, error)
	// ResolveExternalRefs resolves many keys at once; absent keys are missing
	// from the result.
	ResolveExternalRefs(ctx context.Context, keys []domain.GearRefKey) (map[domain.GearRefKey]domain.GearID, error)

	// GetDefault returns the user's default gear for an activity type.
	GetDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType) (domain.GearID, error)
	SetDefault(ctx context.Context, d domain.GearDefault) error
	ClearDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType) error
	ListDefaults(ctx context.Context, userID domain.UserID) ([]domain.GearDefault, error)

	// Repoint moves refs, defaults and activity attachments from one gear to
	// another (same user). Used by the merge-gear use case inside a tx.
	Repoint(ctx context.Context, from, to domain.GearID) error
	// RecomputeTotalsForUser rewrites every gear row of the user from its
	// live (non-deleted) activities' post-override distance and moving time.
	RecomputeTotalsForUser(ctx context.Context, userID domain.UserID) error
	// ListUserIDsWithGear returns the owners of any gear, for the admin rebuild.
	ListUserIDsWithGear(ctx context.Context) ([]domain.UserID, error)
}
