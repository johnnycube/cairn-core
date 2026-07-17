package domain

import "time"

// WebhookEndpoint is a user-configured outbound webhook (webhook_endpoints,
// migration 10). When a notification fires, the dispatcher POSTs a JSON
// payload to every enabled endpoint that subscribes to the notification's
// type+severity, signed with the endpoint's HMAC-SHA256 secret.
//
// SigningSecret holds the plaintext secret only in flight (creation / when the
// repo decrypts it for signing); the column stores it encrypted at rest with
// the instance master key.
type WebhookEndpoint struct {
	ID     WebhookEndpointID
	UserID UserID

	Name string
	URL  string

	// SigningSecret is the plaintext HMAC key. Empty when the row was loaded
	// without decrypting (e.g. list views never expose it).
	SigningSecret        string
	SigningSecretRotated time.Time

	// EventTypes is the subscription filter. Empty = all types.
	EventTypes  []NotificationType
	MinSeverity NotificationSeverity

	Enabled bool

	// Delivery telemetry, maintained by the dispatcher.
	LastDeliveryAt         *time.Time
	LastDeliveryStatusCode int
	LastDeliveryError      string
	ConsecutiveFailures    int

	// AutoDisabled is set after WebhookMaxConsecutiveFailures failures in a
	// row; the user must re-enable. Distinct from Enabled=false (a manual
	// pause) so the UI can explain why it stopped.
	AutoDisabled   bool
	AutoDisabledAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// WebhookMaxConsecutiveFailures is how many back-to-back delivery failures
// auto-disable an endpoint. Bounds noise to a dead URL without dropping a
// transient blip.
const WebhookMaxConsecutiveFailures = 10

// Subscribes reports whether this endpoint should receive a notification of
// the given type and severity. An empty EventTypes list matches all types;
// severity must meet MinSeverity. Disabled/auto-disabled endpoints never
// subscribe (the dispatcher also filters these at the repo level, but this
// keeps the predicate self-contained for tests).
func (e WebhookEndpoint) Subscribes(t NotificationType, sev NotificationSeverity) bool {
	if !e.Enabled || e.AutoDisabled {
		return false
	}
	if severityOrder(sev) < severityOrder(e.MinSeverity) {
		return false
	}
	if len(e.EventTypes) == 0 {
		return true
	}
	for _, et := range e.EventTypes {
		if et == t {
			return true
		}
	}
	return false
}

// severityOrder ranks severities for min-severity gating (mirrors the
// dispatcher's severityRank; kept in domain so Subscribes is pure).
func severityOrder(s NotificationSeverity) int {
	switch s {
	case NotificationSeverityError:
		return 2
	case NotificationSeverityWarn:
		return 1
	default:
		return 0
	}
}
