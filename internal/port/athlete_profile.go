package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// AthleteProfileRepo persists a user's physiological metric time series
// (FTP/weight/HR/…). Entries are keyed by (user_id, metric_key, effective_date)
// so re-saving the same date updates in place.
type AthleteProfileRepo interface {
	// ListEntries returns all of a user's metric entries, any key, ordered by
	// (key, effective_date ASC). Used to build a domain.AthleteProfile.
	ListEntries(ctx context.Context, userID domain.UserID) ([]domain.AthleteMetricEntry, error)

	// UpsertEntry inserts or updates one dated measurement (conflict on
	// user_id+key+effective_date). Returns the row id.
	UpsertEntry(ctx context.Context, e domain.AthleteMetricEntry) (domain.AthleteMetricID, error)

	// DeleteEntry removes one entry by id, scoped to the owning user.
	DeleteEntry(ctx context.Context, userID domain.UserID, id domain.AthleteMetricID) error
}
