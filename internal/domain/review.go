package domain

import "time"

// ActivityReviewItem is the lightweight projection of an activity flagged
// needs_review (medium-confidence merge) for the review queue.
type ActivityReviewItem struct {
	ID              ActivityID
	Title           string
	Type            ActivityType
	StartTime       time.Time
	MatchConfidence string
}
