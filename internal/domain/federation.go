package domain

import "time"

// FederatedActor is a cached remote ActivityPub actor — enough to verify its
// signatures (PublicKeyPEM) and deliver activities back to it (Inbox).
type FederatedActor struct {
	ActorID           string // the AP actor URL (also the id)
	Inbox             string
	SharedInbox       string
	PublicKeyPEM      string
	PreferredUsername string
	Domain            string
	FetchedAt         time.Time
}

// FederationFollowDirection distinguishes a remote-follows-local edge (inbound,
// we are the followee) from a local-follows-remote edge (outbound).
type FederationFollowDirection string

const (
	FederationFollowInbound  FederationFollowDirection = "inbound"
	FederationFollowOutbound FederationFollowDirection = "outbound"
)

// FederationFollowStatus mirrors the Follow/Accept handshake.
type FederationFollowStatus string

const (
	FederationFollowPending  FederationFollowStatus = "pending"
	FederationFollowAccepted FederationFollowStatus = "accepted"
)

// FederationFollow is one cross-instance follow edge.
type FederationFollow struct {
	ID               FederationFollowID
	LocalUserID      UserID
	RemoteActorID    string
	Direction        FederationFollowDirection
	Status           FederationFollowStatus
	FollowActivityID string // the AP Follow activity id (for Undo/dedup)
	CreatedAt        time.Time
}

// BlockedDomain is a defederated remote instance (Phase 5).
type BlockedDomain struct {
	Domain    string
	Reason    string
	CreatedAt time.Time
}

// FederationDeliveryStatus tracks an outbound delivery's lifecycle.
type FederationDeliveryStatus string

const (
	FederationDeliveryPending   FederationDeliveryStatus = "pending"
	FederationDeliveryDelivered FederationDeliveryStatus = "delivered"
	FederationDeliveryDead      FederationDeliveryStatus = "dead" // gave up (max attempts or permanent reject)
)

// FederationDelivery is one queued POST of a signed activity to a remote inbox,
// retried with backoff until delivered or dead.
type FederationDelivery struct {
	ID            FederationDeliveryID
	FromUserID    UserID
	ActorID       string // signing identity (keyId base)
	InboxURL      string
	Body          []byte // the activity JSON to POST
	ActivityAPID  string
	Status        FederationDeliveryStatus
	Attempts      int
	MaxAttempts   int
	NextAttemptAt time.Time
	LastError     string
}

// FederatedFeedItem is a remote actor's activity (workout) received in a local
// user's inbox and surfaced in their home feed.
type FederatedFeedItem struct {
	ID           FederationFeedItemID
	RecipientID  UserID
	ActorID      string
	ActivityAPID string
	Published    time.Time
	Name         string
	Summary      string
	URL          string
	ImageURL     string
	Sport        string
	DistanceM    *float64
	DurationS    *int
	ElevationM   *float64
}
