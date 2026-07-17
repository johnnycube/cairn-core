package domain

import "time"

// FollowStatus is the state of a follow edge. v1 auto-accepts; 'pending'
// reserves space for a future request/accept flow on private profiles.
type FollowStatus string

const (
	FollowStatusPending  FollowStatus = "pending"
	FollowStatusAccepted FollowStatus = "accepted"
)

// Follow is a directed edge: Follower follows Followee.
type Follow struct {
	FollowerID UserID
	FolloweeID UserID
	Status     FollowStatus
	CreatedAt  time.Time
}

// FollowCounts summarises a user's graph position.
type FollowCounts struct {
	Followers int
	Following int
}
