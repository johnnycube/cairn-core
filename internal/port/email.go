package port

import "context"

// EmailMessage is a single outbound email. HTMLBody is optional; when set the
// message is sent multipart/alternative (text + html), otherwise text/plain.
type EmailMessage struct {
	To       string
	Subject  string
	TextBody string
	HTMLBody string // optional
}

// EmailSender delivers transactional email (verification, password reset,
// notification channels). Implementations are selected by EMAIL_ADAPTER
// (smtp, ses, …); a nil EmailSender means email is disabled (EMAIL_ADAPTER=none)
// and callers skip the email channel.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}
