package domain

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// ExternalAccount
//
// One row per (user, provider, provider_account_id). Mirrors the
// external_accounts table from migration 4. Multiple accounts of the
// same provider per user are supported (e.g., two Strava accounts on
// one Cairn user).
//
// Token columns (access_token_encrypted, refresh_token_encrypted) are
// NOT exposed at the domain level — encryption/decryption lives in the
// secondary adapter, and callers either receive a fresh access_token
// through the token-fetch RPC (see docs/architecture.md §6) or never
// see one. This is enforced by NOT having the token fields on this
// struct.
// ---------------------------------------------------------------------------

type ExternalAccount struct {
	ID                ExternalAccountID
	UserID            UserID
	Provider          string
	ProviderAccountID string // empty until first OAuth callback resolves it

	// ConnectionID links the account to the connection (OAuth-app credentials)
	// that produced it. nil for legacy rows. The token-refresh path resolves
	// this connection's client_id/secret.
	ConnectionID *ConnectionID

	DisplayLabel string

	// AssignedWorkerName empty means "use the current primary worker
	// for this provider". Set by an admin when pinning an account for
	// canary migration of a new worker version.
	AssignedWorkerName string

	Status       ExternalAccountStatus
	StatusReason string

	// AutoImportEnabled gates automatic imports (reconcile + webhook fetch).
	// When false the account stays linked but the scheduler/webhook skip it.
	AutoImportEnabled bool

	// Config holds non-secret values keyed by ConfigField.key from the
	// provider manifest. Values can be strings (TEXT, SELECT) or string
	// slices (MULTI_SELECT) — hence the json.RawMessage rather than
	// map[string]string.
	Config json.RawMessage

	GrantedScopes []string

	WebhookSubscribed     bool
	WebhookSubscribedAt   *time.Time
	WebhookSubscriptionID string

	// RateLimit is the snapshot from the most recent worker API call.
	// Used to expose to operators ("Strava rate-limited until 17:34")
	// and as input to the scheduler (skip accounts in cooldown).
	RateLimit *RateLimitSnapshot

	// SyncWatermark is the timestamp the next incremental import should
	// pass as `since`. Updated after a successful reconcile to the
	// latest activity start_time we observed. nil means "do a full
	// backfill from the beginning".
	SyncWatermark        *time.Time
	LastSyncAt           *time.Time
	LastSuccessfulSyncAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExternalAccountStatus mirrors the CHECK constraint in migration 4.
type ExternalAccountStatus string

const (
	ExternalAccountStatusActive         ExternalAccountStatus = "active"
	ExternalAccountStatusAuthInvalid    ExternalAccountStatus = "auth_invalid"
	ExternalAccountStatusWorkerOffline  ExternalAccountStatus = "worker_offline"
	ExternalAccountStatusRateLimited    ExternalAccountStatus = "rate_limited"
	ExternalAccountStatusNeedsMigration ExternalAccountStatus = "needs_migration"
	ExternalAccountStatusDisabled       ExternalAccountStatus = "disabled"
)

func (s ExternalAccountStatus) Valid() bool {
	switch s {
	case ExternalAccountStatusActive,
		ExternalAccountStatusAuthInvalid,
		ExternalAccountStatusWorkerOffline,
		ExternalAccountStatusRateLimited,
		ExternalAccountStatusNeedsMigration,
		ExternalAccountStatusDisabled:
		return true
	}
	return false
}

// IsEligibleForSync returns true when the account should be considered
// by the reconciler. Auth-invalid and disabled accounts are skipped;
// rate-limited accounts are skipped until their snapshot indicates
// the limit has reset.
func (a ExternalAccount) IsEligibleForSync(now time.Time) bool {
	if !a.AutoImportEnabled {
		return false
	}
	switch a.Status {
	case ExternalAccountStatusActive:
		return true
	case ExternalAccountStatusRateLimited:
		if a.RateLimit == nil {
			return true // unknown reset; let the worker retry and decide
		}
		return now.After(a.RateLimit.ShortWindowResetAt) &&
			now.After(a.RateLimit.LongWindowResetAt)
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// RateLimitSnapshot
//
// Provider-reported rate-limit state. Two windows because Strava (and
// most others) enforce both a short rolling window and a daily quota.
// Garmin doesn't report this in headers — its adapter derives the
// snapshot from a heuristic counter.
// ---------------------------------------------------------------------------

type RateLimitSnapshot struct {
	ShortWindowLimit   int
	ShortWindowUsed    int
	ShortWindowResetAt time.Time
	LongWindowLimit    int
	LongWindowUsed     int
	LongWindowResetAt  time.Time
	Reported           time.Time // when the worker last observed this
}

// ShortRemaining is a convenience.
func (r RateLimitSnapshot) ShortRemaining() int {
	if r.ShortWindowUsed >= r.ShortWindowLimit {
		return 0
	}
	return r.ShortWindowLimit - r.ShortWindowUsed
}

// LongRemaining is a convenience.
func (r RateLimitSnapshot) LongRemaining() int {
	if r.LongWindowUsed >= r.LongWindowLimit {
		return 0
	}
	return r.LongWindowLimit - r.LongWindowUsed
}
