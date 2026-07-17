package domain

import (
	"time"

	"github.com/google/uuid"
)

// ImportQueueItemID identifies a row in the persisted import queue.
type ImportQueueItemID uuid.UUID

func (id ImportQueueItemID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id ImportQueueItemID) String() string  { return uuid.UUID(id).String() }

// ImportItemType is the kind of entity a queue row imports.
type ImportItemType string

const (
	ImportItemActivity ImportItemType = "activity"
	ImportItemSegment  ImportItemType = "segment"
	ImportItemMetric   ImportItemType = "metric"
)

// ImportItemStatus is the lifecycle of a queue row.
type ImportItemStatus string

const (
	ImportStatusPending    ImportItemStatus = "pending"
	ImportStatusInProgress ImportItemStatus = "in_progress"
	ImportStatusDone       ImportItemStatus = "done"
	ImportStatusFailed     ImportItemStatus = "failed"
	ImportStatusSkipped    ImportItemStatus = "skipped"
)

// ImportQueueItem is one unit of import work discovered during a full sync.
// The core processor dequeues pending items and dispatches a fetch job to
// the worker; ingest dedups on (provider, external_id) — the same key here.
type ImportQueueItem struct {
	ID                ImportQueueItemID
	ExternalAccountID ExternalAccountID
	UserID            UserID
	Provider          string

	ItemType   ImportItemType
	ExternalID string
	ItemTime   *time.Time // e.g. activity start, for newest-first ordering

	Status    ImportItemStatus
	Priority  int // manual "move to top": claimed before lower priorities, 0 = normal
	Attempts  int
	LastError string

	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}
