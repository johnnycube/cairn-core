package domain

import "time"

// Social-engagement aggregates (multi-user v1): share links, kudos, comments.
// All are gated by the visibility model — a viewer may only kudos/comment when
// the owner's policy grants them CategorySocial for their resolved audience.

// ShareLink grants session-less, read-only access to one activity via an
// unguessable token. A revoked link (RevokedAt != nil) no longer resolves.
type ShareLink struct {
	Token      string
	ActivityID ActivityID
	CreatedBy  UserID
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

// Active reports whether the link still grants access.
func (l ShareLink) Active() bool { return l.RevokedAt == nil }

// Kudos is a single "nice work" from one user on one activity.
type Kudos struct {
	ActivityID ActivityID
	UserID     UserID
	CreatedAt  time.Time
}

// Comment is a user's text comment on an activity.
type Comment struct {
	ID         CommentID
	ActivityID ActivityID
	UserID     UserID
	Body       string
	CreatedAt  time.Time
}

// MaxCommentLength bounds a single comment body.
const MaxCommentLength = 2000
