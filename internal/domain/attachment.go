package domain

import "time"

// Attachment is a photo/media item belonging to an activity — a first-class
// entity, not a merged field. Bytes live in blob storage (BlobID, an S3 key);
// ExternalURL records the original provider URL for provenance. Provider-
// imported attachments carry the SourceID they came from (so a reimport can
// replace exactly that source's set); user uploads have SourceID == nil.
type Attachment struct {
	ID          AttachmentID
	ActivityID  ActivityID
	SourceID    *SourceID
	UserID      UserID
	BlobID      string
	ExternalURL string
	ContentType string
	Caption     string
	Width       int
	Height      int
	Position    int
	CreatedAt   time.Time
}
