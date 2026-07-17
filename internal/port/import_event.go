package port

import (
	"context"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ImportEventRepo persists a per-connection import history. Recording is
// best-effort from the caller's perspective: a failure to log must never fail
// the underlying import.
type ImportEventRepo interface {
	// Record appends one event. ID/OccurredAt are assigned if zero.
	Record(ctx context.Context, ev domain.ConnectionImportEvent) error

	// ListForAccount returns the newest `limit` events for the account.
	ListForAccount(
		ctx context.Context,
		accountID domain.ExternalAccountID,
		limit int,
	) ([]domain.ConnectionImportEvent, error)
}
