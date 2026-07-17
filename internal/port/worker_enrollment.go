package port

import (
	"context"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ---------------------------------------------------------------------------
// WorkerEnrollmentRepo
//
// Persists worker enrollment offers and the credential-grant audit trail.
// Implementations live under internal/adapter/secondary/postgres/.
//
// Token plaintext is never stored — callers pass the sha256 hash. The
// helper sha256.Sum256([]byte(plain)) is the canonical way to produce
// it (use internal/auth.HashEnrollmentToken to ensure consistency).
// ---------------------------------------------------------------------------

type WorkerEnrollmentRepo interface {
	// CreateEnrollment inserts a new offer with the given token hash.
	// Returns ErrTokenHashConflict if the hash already exists — caller
	// should generate a fresh token (extremely improbable with 256-bit
	// entropy, but defended for completeness).
	CreateEnrollment(ctx context.Context, e domain.WorkerEnrollment) (domain.WorkerEnrollment, error)

	// GetEnrollment by primary key. Used by admin endpoints (list, show).
	GetEnrollment(ctx context.Context, id domain.WorkerEnrollmentID) (domain.WorkerEnrollment, error)

	// FindEnrollmentByTokenHash is the auth-callout hot path. Must return
	// in <5ms — index on token_hash is unique so it's a single index scan.
	// Returns domain.ErrEnrollmentNotFound if no match.
	FindEnrollmentByTokenHash(ctx context.Context, hash []byte) (domain.WorkerEnrollment, error)

	// ListEnrollments returns enrollments matching the filter. Used by
	// admin UI. Pagination via opaque cursor (id-based) in the future;
	// v1 is unbounded for simplicity.
	ListEnrollments(ctx context.Context, filter ListEnrollmentsFilter) ([]domain.WorkerEnrollment, error)

	// IncrementUses atomically bumps the uses counter. Called inside the
	// auth-callout transaction. Returns the post-increment uses value so
	// the caller can log "uses 3 of 5".
	IncrementUses(ctx context.Context, id domain.WorkerEnrollmentID) (newUses int, err error)

	// RevokeEnrollment marks the offer as revoked. The auth-callout will
	// refuse future authentications even if the token is still valid by
	// expiry/max-uses. Existing connections are kicked by the separate
	// RevokeGrant path.
	RevokeEnrollment(
		ctx context.Context,
		id domain.WorkerEnrollmentID,
		by *domain.UserID,
		reason string,
		at time.Time,
	) error

	// ExtendEnrollment sets a new expiry — the admin "prolong" action. Works
	// even after the enrollment lapsed (re-arm an expired worker). Does not
	// affect revocation.
	ExtendEnrollment(ctx context.Context, id domain.WorkerEnrollmentID, newExpiresAt time.Time) error

	// PurgeExpiredEnrollments deletes rows where expires_at < now AND
	// the enrollment was never used. Called by a periodic cleanup job
	// to keep the table bounded. Used enrollments are kept for audit.
	PurgeExpiredEnrollments(ctx context.Context, before time.Time) (deleted int, err error)

	// ---------- credential grants (audit trail) ----------

	// CreateGrant records a successful auth-callout admission. Called
	// inside the same transaction as IncrementUses.
	CreateGrant(ctx context.Context, g domain.WorkerCredentialGrant) (domain.WorkerCredentialGrant, error)

	// FindGrantByNKey is consulted on every connect: if a grant exists
	// and is revoked, refuse the connection even if the underlying
	// enrollment is still valid. Returns ErrGrantNotFound if no row.
	FindGrantByNKey(ctx context.Context, userNKeyPublic string) (domain.WorkerCredentialGrant, error)

	// TouchGrant updates last_seen_at. Called from the heartbeat watcher
	// when a worker's presence is observed; gives operators a view of
	// "which credentials are actively in use".
	TouchGrant(ctx context.Context, id domain.WorkerCredentialGrantID, at time.Time) error

	// RevokeGrant marks a grant as revoked. Subsequent reconnects with
	// the same user-nkey are refused. The currently-open connection is
	// not killed by this call — operators run a separate "kick" command
	// (cairn worker disconnect) to do that via cairn.cmd.worker.<name>.shutdown.
	RevokeGrant(
		ctx context.Context,
		id domain.WorkerCredentialGrantID,
		by *domain.UserID,
		reason string,
		at time.Time,
	) error

	ListGrantsForEnrollment(ctx context.Context, id domain.WorkerEnrollmentID) ([]domain.WorkerCredentialGrant, error)
}

// ErrTokenHashConflict is returned by CreateEnrollment when sha256(token)
// collides with an existing row. Caller should re-generate.
var ErrTokenHashConflict = errInternal("enrollment token hash conflict")

// ErrGrantNotFound is returned by FindGrantByNKey when no row exists. Not
// an error condition for first-time connects — the caller distinguishes.
var ErrGrantNotFound = errInternal("worker credential grant not found")

// errInternal is a tiny error helper to avoid sucking in a dependency
// just to declare two sentinels.
type errInternal string

func (e errInternal) Error() string { return string(e) }

// ListEnrollmentsFilter narrows the result set for admin listings.
type ListEnrollmentsFilter struct {
	Provider       string // empty = all providers
	IncludeRevoked bool
	IncludeExpired bool
	Limit          int // 0 = no limit (admin only)
}

// ---------------------------------------------------------------------------
// SigningKeyRepo
//
// Manages the cairn-server's NATS account signing keys. There is normally
// exactly one active key per purpose; rotation inserts a second active
// key, NATS-server config is updated to trust both account public keys,
// the old key is deactivated, eventually removed from NATS config.
// ---------------------------------------------------------------------------

type SigningKeyRepo interface {
	// GetActive returns the currently-active key for the purpose. Returns
	// domain.ErrNotFound if no key exists — bootstrap must run.
	GetActive(ctx context.Context, purpose string) (domain.SigningKey, error)

	// CreateKey inserts a new key. If `active=true`, the caller must
	// first DeactivateOthers to satisfy the partial unique index.
	CreateKey(ctx context.Context, k domain.SigningKey) (domain.SigningKey, error)

	// DeactivateAll for the purpose. Called as part of key rotation.
	DeactivateAll(ctx context.Context, purpose string, at time.Time) error
}

// ---------------------------------------------------------------------------
// NATSCredentialIssuer
//
// Mints NATS user JWTs at auth-callout time. The implementation lives
// in adapter/secondary/nats/credential_issuer.go and uses
// github.com/nats-io/jwt/v2 + github.com/nats-io/nkeys to sign with the
// cairn account signing key from SigningKeyRepo.
//
// The issuer is the ONLY component in the codebase that handles the
// decrypted account seed. Use cases call this interface; they never see
// raw key material.
// ---------------------------------------------------------------------------

type NATSCredentialIssuer interface {
	// IssueWorkerCredential mints a User JWT scoped to the given
	// permissions. The worker provides its own ephemeral user-NKey
	// (generated in-process at connect time); the issuer never sees the
	// worker's seed.
	//
	// Returns the encoded JWT and the issuer-side metadata (expires_at)
	// the caller writes to worker_credential_grants.
	IssueWorkerCredential(ctx context.Context, in IssueCredentialInput) (IssueCredentialOutput, error)

	// AccountPublicKey returns the NATS account public key the issuer
	// signs with. Used by operators to populate the NATS-server config
	// (`resolver`/`trusted_keys`). Cached server-side; cheap to call.
	AccountPublicKey(ctx context.Context) (string, error)
}

type IssueCredentialInput struct {
	// UserNKeyPublic is the public form of the Ed25519 keypair the worker
	// generated. Must start with "U" and be 56 chars long (NATS encoding).
	// The JWT's `sub` claim is this value.
	UserNKeyPublic string

	// Permissions to embed in the JWT. The auth-callout handler computes
	// this from the enrollment + provider before calling the issuer.
	Permissions domain.WorkerPermissionTemplate

	// TTL controls the JWT's `exp` claim. The auth-callout passes a
	// duration in the 6h–24h range — long enough that token-refresh
	// isn't constant, short enough that revocation has bounded effect
	// before the next reconnect.
	TTL time.Duration

	// Bearer, when true, marks the JWT as a bearer token (no signature
	// challenge during connect). Used for HTTP-fallback enrollment where
	// the worker doesn't have an NKey seed; in the primary callout path
	// it's false.
	Bearer bool
}

type IssueCredentialOutput struct {
	// UserJWT is the signed, encoded JWT the worker passes to NATS via
	// nats.UserJWT(...) callback or as part of the auth-callout response.
	UserJWT string

	// ExpiresAt is the JWT's `exp` claim resolved to wall-clock time.
	// Stored in worker_credential_grants for the audit trail.
	ExpiresAt time.Time

	// AccountPublicKey is the same value as AccountPublicKey() above,
	// included for convenience so the caller doesn't need a second call.
	AccountPublicKey string
}

// IssueValidationError is returned when an input to IssueWorkerCredential
// fails validation (bad NKey format, etc.). Adapters return this rather
// than a free-form error so the auth-callout subscriber can map it to a
// stable rejection reason.
type IssueValidationError struct {
	Field   string
	Message string
}

func (e *IssueValidationError) Error() string {
	return "nats credential issuer: invalid " + e.Field + ": " + e.Message
}
