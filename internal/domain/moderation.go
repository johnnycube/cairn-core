package domain

import (
	"time"

	"github.com/google/uuid"
)

// User blocking + content moderation (multi-user v1).

// ReportTargetKind is what a content report points at.
type ReportTargetKind string

const (
	ReportTargetActivity ReportTargetKind = "activity"
	ReportTargetComment  ReportTargetKind = "comment"
	ReportTargetUser     ReportTargetKind = "user"
)

func (k ReportTargetKind) Valid() bool {
	switch k {
	case ReportTargetActivity, ReportTargetComment, ReportTargetUser:
		return true
	}
	return false
}

// ReportStatus is the moderation lifecycle of a report.
type ReportStatus string

const (
	ReportOpen      ReportStatus = "open"
	ReportReviewed  ReportStatus = "reviewed"
	ReportDismissed ReportStatus = "dismissed"
)

// ContentReport is a user's report of an activity / comment / user.
type ContentReport struct {
	ID         ContentReportID
	ReporterID UserID
	TargetKind ReportTargetKind
	TargetID   uuid.UUID
	Reason     string
	Status     ReportStatus
	CreatedAt  time.Time
	ReviewedAt *time.Time
	ReviewedBy *UserID
}

// MaxReportReasonLength bounds a report reason.
const MaxReportReasonLength = 1000
