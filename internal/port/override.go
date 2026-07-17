package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FieldOverrideRepo persists per-field manual source pins (brief §13.1).
type FieldOverrideRepo interface {
	// ListForActivity returns all field overrides for an activity.
	ListForActivity(ctx context.Context, activityID domain.ActivityID) ([]domain.FieldSourceOverride, error)
	// Set upserts a pin for (activity, field_key).
	Set(ctx context.Context, o domain.FieldSourceOverride) error
	// Delete clears the pin for (activity, field_key). No-op if absent.
	Delete(ctx context.Context, activityID domain.ActivityID, fieldKey domain.FieldGroup) error
}

// ClassificationOverrideRepo persists the per-activity user classification
// overlay (type/discipline/flags/custom_subtype). Applied after the merge on
// every recompute so user corrections survive re-derivation.
type ClassificationOverrideRepo interface {
	// Get returns the overlay for an activity. A zero-value (all-nil) override
	// and nil error means "no overrides set".
	Get(ctx context.Context, activityID domain.ActivityID) (domain.ClassificationOverride, error)
	// Set upserts the overlay. An Empty() override clears the row.
	Set(ctx context.Context, o domain.ClassificationOverride) error
}

// SourceDenylistRepo persists detach decisions so a re-pushed source does not
// silently re-attach (Gap 6).
type SourceDenylistRepo interface {
	Add(ctx context.Context, e domain.SourceDenylistEntry) error
	// IsDenied reports whether the (provider, account, external_id) tuple is on
	// the user's denylist.
	IsDenied(ctx context.Context, userID domain.UserID, provider string, externalAccountID *domain.ExternalAccountID, externalID string) (bool, error)
}
