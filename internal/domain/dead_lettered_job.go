package domain

import (
	"time"

	"github.com/google/uuid"
)

// DeadLetteredJobID is the surrogate key for a DLQ row.
type DeadLetteredJobID uuid.UUID

func (id DeadLetteredJobID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id DeadLetteredJobID) String() string  { return uuid.UUID(id).String() }

// DeadLetteredJob is one entry in the dead-letter queue, populated when
// a JetStream consumer exhausts MaxDeliver and NATS emits the
// $JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES advisory.
//
// Operationally a flat record: no business logic, no invariants beyond
// "this row reflects one terminal message failure". Lives in the
// domain package because the admin endpoints expose it on the wire
// (architecture.md §7.4).
type DeadLetteredJob struct {
	ID DeadLetteredJobID

	// Origin in JetStream — operators use these to map back to a
	// specific consumer / stream when investigating.
	Stream   string
	Subject  string
	Consumer string

	// MsgID is the Nats-Msg-Id header, when set by the producer.
	MsgID string

	// Payload is the message body, captured verbatim from JetStream
	// before Term() evicted it. May be empty if the advisory beat us
	// to the fetch.
	Payload []byte

	// Headers from the dead message, frozen at the time of capture.
	Headers map[string]string

	// DeliveredCount is how many times JetStream tried (and the worker
	// either NAKed or Termed) before giving up.
	DeliveredCount int

	// LastError is whatever the worker reported on its final failure.
	// Free-form; safe to expose in admin UI.
	LastError string

	FirstSeenAt time.Time
	LastSeenAt  time.Time

	// Replay state. ReplayedAt = nil → never replayed.
	ReplayedAt       *time.Time
	ReplayedByUserID *UserID
	ReplayCount      int
}

// IsReplayed reports whether this row has been replayed at least once.
func (j DeadLetteredJob) IsReplayed() bool { return j.ReplayedAt != nil }
