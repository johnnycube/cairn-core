package domain

import (
	"time"

	"github.com/google/uuid"
)

// ConnectionImportEventID identifies an import-history entry.
type ConnectionImportEventID uuid.UUID

func (id ConnectionImportEventID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id ConnectionImportEventID) String() string  { return uuid.UUID(id).String() }

// ConnectionImportEvent is one entry in a connection's (external account's)
// import history — a sync start, an activity import, or a failure.
type ConnectionImportEvent struct {
	ID                ConnectionImportEventID
	ExternalAccountID ExternalAccountID
	Kind              string // sync_started | activity_imported | activity_updated | failed
	Count             int
	Detail            string
	ExternalID        string
	OccurredAt        time.Time
}

// Event kind constants.
const (
	ImportEventSyncStarted      = "sync_started"
	ImportEventActivityImported = "activity_imported"
	ImportEventActivityUpdated  = "activity_updated"
	ImportEventFailed           = "failed"
)
