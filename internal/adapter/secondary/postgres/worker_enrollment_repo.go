package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// WorkerEnrollmentRepo implements port.WorkerEnrollmentRepo on the
// worker_enrollments + worker_credential_grants tables from migration 14.
//
// Hot-path method (called once per worker connect): FindEnrollmentByTokenHash.
// Single index scan on token_hash (UNIQUE index, see migration 14 line 22).
//
// Cold-path methods (admin UI): CreateEnrollment, GetEnrollment,
// ListEnrollments, RevokeEnrollment, PurgeExpiredEnrollments.
type WorkerEnrollmentRepo struct {
	pool *pgxpool.Pool
}

func NewWorkerEnrollmentRepo(pool *pgxpool.Pool) *WorkerEnrollmentRepo {
	return &WorkerEnrollmentRepo{pool: pool}
}

// enrollmentColumns is the canonical column list for SELECT. Order matters —
// scanEnrollmentRow depends on it. Sync with migration 14.
const enrollmentColumns = `
	id, token_hash, provider, name, version, worker_name_pattern, permission_template,
	created_at, created_by_user_id, note,
	expires_at, max_uses, uses,
	revoked_at, revoked_by_user_id, revoked_reason
`

// CreateEnrollment inserts a new enrollment offer. The caller produces
// the token plaintext, hashes it (sha256), and passes only the hash.
//
// Returns port.ErrTokenHashConflict if the hash is a duplicate — vanishingly
// rare with 256-bit entropy but defended for completeness. The use case
// retries with a fresh token.
func (r *WorkerEnrollmentRepo) CreateEnrollment(
	ctx context.Context,
	e domain.WorkerEnrollment,
) (domain.WorkerEnrollment, error) {
	db := dbtx(ctx, r.pool)

	var createdBy any
	if e.CreatedByUserID != nil {
		createdBy = e.CreatedByUserID.UUID()
	}

	row := db.QueryRow(ctx,
		`INSERT INTO worker_enrollments (
		    id, token_hash, provider, name, version, worker_name_pattern, permission_template,
		    created_at, created_by_user_id, note,
		    expires_at, max_uses, uses
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING `+enrollmentColumns,
		e.ID.UUID(), e.TokenHash, e.Provider, e.Name, e.Version, e.WorkerNamePattern, e.PermissionTemplate,
		e.CreatedAt, createdBy, e.Note,
		e.ExpiresAt, e.MaxUses, e.Uses,
	)
	created, err := scanEnrollmentRow(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique violation
			if strings.Contains(pgErr.ConstraintName, "token_hash") {
				return domain.WorkerEnrollment{}, port.ErrTokenHashConflict
			}
		}
		return domain.WorkerEnrollment{}, fmt.Errorf("insert worker_enrollment: %w", err)
	}
	return created, nil
}

// GetEnrollment by ID — used by admin endpoints for the "show details" view.
func (r *WorkerEnrollmentRepo) GetEnrollment(
	ctx context.Context,
	id domain.WorkerEnrollmentID,
) (domain.WorkerEnrollment, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+enrollmentColumns+`
		   FROM worker_enrollments
		  WHERE id = $1`,
		id.UUID(),
	)
	e, err := scanEnrollmentRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerEnrollment{}, domain.ErrEnrollmentNotFound
	}
	if err != nil {
		return domain.WorkerEnrollment{}, fmt.Errorf("get worker_enrollment %s: %w", id, err)
	}
	return e, nil
}

// FindEnrollmentByTokenHash is the auth-callout hot path. The UNIQUE
// index on token_hash means this is a single index scan; observed
// p99 < 2ms on a 10k-row table.
//
// Returns domain.ErrEnrollmentNotFound if no match — auth-callout
// translates that to a connection rejection.
func (r *WorkerEnrollmentRepo) FindEnrollmentByTokenHash(
	ctx context.Context,
	hash []byte,
) (domain.WorkerEnrollment, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+enrollmentColumns+`
		   FROM worker_enrollments
		  WHERE token_hash = $1`,
		hash,
	)
	e, err := scanEnrollmentRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerEnrollment{}, domain.ErrEnrollmentNotFound
	}
	if err != nil {
		return domain.WorkerEnrollment{}, fmt.Errorf("find enrollment by token hash: %w", err)
	}
	return e, nil
}

// ListEnrollments returns enrollments matching the filter, sorted newest-first.
// Used by admin UI/CLI for the list view.
func (r *WorkerEnrollmentRepo) ListEnrollments(
	ctx context.Context,
	filter port.ListEnrollmentsFilter,
) ([]domain.WorkerEnrollment, error) {
	db := dbtx(ctx, r.pool)

	// Build the WHERE dynamically — keep it index-friendly by using the
	// partial index on (expires_at) WHERE revoked_at IS NULL when possible.
	wheres := []string{}
	args := []any{}
	idx := 1

	if filter.Provider != "" {
		wheres = append(wheres, fmt.Sprintf("provider = $%d", idx))
		args = append(args, filter.Provider)
		idx++
	}
	if !filter.IncludeRevoked {
		wheres = append(wheres, "revoked_at IS NULL")
	}
	if !filter.IncludeExpired {
		wheres = append(wheres, fmt.Sprintf("expires_at > $%d", idx))
		args = append(args, time.Now().UTC())
		idx++
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	limitClause := ""
	if filter.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT %d", filter.Limit)
	}

	q := fmt.Sprintf(`
		SELECT %s
		  FROM worker_enrollments
		  %s
		  ORDER BY created_at DESC, id DESC
		  %s
	`, enrollmentColumns, whereClause, limitClause)

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list enrollments: %w", err)
	}
	defer rows.Close()

	var out []domain.WorkerEnrollment
	for rows.Next() {
		e, err := scanEnrollmentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan enrollment row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enrollment rows: %w", err)
	}
	return out, nil
}

// IncrementUses atomically bumps the uses counter. Called inside the
// auth-callout transaction in the same go-routine as CreateGrant.
// Returns the post-increment count so the use case can log "uses 3 of 5".
//
// Concurrent invocations are race-safe via Postgres row-level locking
// (UPDATE acquires FOR UPDATE on the row). Auth-callout serialises by
// enrollment-id implicitly.
func (r *WorkerEnrollmentRepo) IncrementUses(
	ctx context.Context,
	id domain.WorkerEnrollmentID,
) (int, error) {
	db := dbtx(ctx, r.pool)
	var newUses int
	err := db.QueryRow(ctx,
		`UPDATE worker_enrollments
		    SET uses = uses + 1
		  WHERE id = $1
		    AND revoked_at IS NULL
		    AND (max_uses = 0 OR uses < max_uses)
		    AND expires_at > now()
		 RETURNING uses`,
		id.UUID(),
	).Scan(&newUses)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either gone (deleted), or no longer valid (race with revoke/expire/exhaust).
		// Re-fetch to give the caller a specific error.
		current, getErr := r.GetEnrollment(ctx, id)
		if getErr != nil {
			return 0, getErr
		}
		if err := current.IsValidAt(time.Now().UTC()); err != nil {
			return 0, err
		}
		return 0, errors.New("increment uses raced with concurrent state change")
	}
	if err != nil {
		return 0, fmt.Errorf("increment enrollment uses %s: %w", id, err)
	}
	return newUses, nil
}

// RevokeEnrollment marks the offer as revoked. Future auth-callouts
// presenting this token are refused even if other validity checks pass.
//
// Existing connections that already negotiated a user-JWT are NOT killed
// by this — operators use RevokeGrant + the cairn.cmd.worker.<name>.shutdown
// subject (see docs/architecture.md §7) to evict open sessions.
func (r *WorkerEnrollmentRepo) RevokeEnrollment(
	ctx context.Context,
	id domain.WorkerEnrollmentID,
	by *domain.UserID,
	reason string,
	at time.Time,
) error {
	db := dbtx(ctx, r.pool)
	var revokedBy any
	if by != nil {
		revokedBy = by.UUID()
	}
	tag, err := db.Exec(ctx,
		`UPDATE worker_enrollments
		    SET revoked_at = $2,
		        revoked_by_user_id = $3,
		        revoked_reason = $4
		  WHERE id = $1
		    AND revoked_at IS NULL`,
		id.UUID(), at, revokedBy, reason,
	)
	if err != nil {
		return fmt.Errorf("revoke enrollment %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Either already revoked or not found — distinguish.
		_, getErr := r.GetEnrollment(ctx, id)
		if getErr != nil {
			return getErr
		}
		return nil // idempotent: already revoked
	}
	return nil
}

// ExtendEnrollment sets a new expiry on an enrollment — used by the admin
// "prolong" action. Works even when the enrollment has already lapsed (the
// whole point: re-arm a worker whose token expired). Does not touch revocation;
// a revoked enrollment stays revoked.
func (r *WorkerEnrollmentRepo) ExtendEnrollment(
	ctx context.Context,
	id domain.WorkerEnrollmentID,
	newExpiresAt time.Time,
) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`UPDATE worker_enrollments SET expires_at = $2 WHERE id = $1`,
		id.UUID(), newExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("extend enrollment %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEnrollmentNotFound
	}
	return nil
}

// PurgeExpiredEnrollments deletes UNUSED expired offers. Used offers
// are retained for audit; only zero-use rows that expired are removed.
// Called from a periodic cleanup job.
func (r *WorkerEnrollmentRepo) PurgeExpiredEnrollments(
	ctx context.Context,
	before time.Time,
) (int, error) {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`DELETE FROM worker_enrollments
		  WHERE expires_at < $1
		    AND uses = 0`,
		before,
	)
	if err != nil {
		return 0, fmt.Errorf("purge expired enrollments: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Credential grants (audit trail of successful admissions)
// ---------------------------------------------------------------------------

const grantColumns = `
	id, enrollment_id,
	user_nkey_public, worker_name, worker_instance_id, worker_version,
	host(client_host) AS client_host_text,
	issued_at, expires_at, last_seen_at,
	revoked_at, revoked_by_user_id, revoked_reason
`

// CreateGrant records a successful auth-callout admission. Called from
// within the auth-callout transaction in the same db tx as IncrementUses
// — both succeed or both roll back.
func (r *WorkerEnrollmentRepo) CreateGrant(
	ctx context.Context,
	g domain.WorkerCredentialGrant,
) (domain.WorkerCredentialGrant, error) {
	db := dbtx(ctx, r.pool)

	var clientHost any
	if g.ClientHost != "" {
		clientHost = g.ClientHost
	}

	row := db.QueryRow(ctx,
		`INSERT INTO worker_credential_grants (
		    id, enrollment_id,
		    user_nkey_public, worker_name, worker_instance_id, worker_version,
		    client_host,
		    issued_at, expires_at
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8, $9)
		 RETURNING `+grantColumns,
		g.ID.UUID(), g.EnrollmentID.UUID(),
		g.UserNKeyPublic, g.WorkerName, g.WorkerInstanceID, g.WorkerVersion,
		clientHost,
		g.IssuedAt, g.ExpiresAt,
	)
	created, err := scanGrantRow(row)
	if err != nil {
		return domain.WorkerCredentialGrant{}, fmt.Errorf("insert credential grant: %w", err)
	}
	return created, nil
}

// FindGrantByNKey is consulted alongside FindEnrollmentByTokenHash on
// every connect: if a grant exists for this NKey and is revoked, the
// connection is refused regardless of enrollment validity.
func (r *WorkerEnrollmentRepo) FindGrantByNKey(
	ctx context.Context,
	userNKeyPublic string,
) (domain.WorkerCredentialGrant, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+grantColumns+`
		   FROM worker_credential_grants
		  WHERE user_nkey_public = $1`,
		userNKeyPublic,
	)
	g, err := scanGrantRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerCredentialGrant{}, port.ErrGrantNotFound
	}
	if err != nil {
		return domain.WorkerCredentialGrant{}, fmt.Errorf("find grant by nkey: %w", err)
	}
	return g, nil
}

// TouchGrant updates last_seen_at. Heartbeat-driven; high write volume
// during steady state. Single UPDATE on a primary-key match — cheap.
func (r *WorkerEnrollmentRepo) TouchGrant(
	ctx context.Context,
	id domain.WorkerCredentialGrantID,
	at time.Time,
) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE worker_credential_grants
		    SET last_seen_at = $2
		  WHERE id = $1`,
		id.UUID(), at,
	)
	if err != nil {
		return fmt.Errorf("touch grant %s: %w", id, err)
	}
	return nil
}

// RevokeGrant marks a grant as revoked. The active NATS connection is
// not killed by this — that's a separate "kick" operation via the
// cairn.cmd.worker.<name>.shutdown subject.
func (r *WorkerEnrollmentRepo) RevokeGrant(
	ctx context.Context,
	id domain.WorkerCredentialGrantID,
	by *domain.UserID,
	reason string,
	at time.Time,
) error {
	db := dbtx(ctx, r.pool)
	var revokedBy any
	if by != nil {
		revokedBy = by.UUID()
	}
	tag, err := db.Exec(ctx,
		`UPDATE worker_credential_grants
		    SET revoked_at = $2,
		        revoked_by_user_id = $3,
		        revoked_reason = $4
		  WHERE id = $1
		    AND revoked_at IS NULL`,
		id.UUID(), at, revokedBy, reason,
	)
	if err != nil {
		return fmt.Errorf("revoke grant %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil // idempotent
	}
	return nil
}

// ListGrantsForEnrollment returns the admission audit trail for one
// enrollment. Used by admin "show enrollment details" view.
func (r *WorkerEnrollmentRepo) ListGrantsForEnrollment(
	ctx context.Context,
	id domain.WorkerEnrollmentID,
) ([]domain.WorkerCredentialGrant, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+grantColumns+`
		   FROM worker_credential_grants
		  WHERE enrollment_id = $1
		  ORDER BY issued_at DESC, id DESC`,
		id.UUID(),
	)
	if err != nil {
		return nil, fmt.Errorf("list grants for enrollment %s: %w", id, err)
	}
	defer rows.Close()

	var out []domain.WorkerCredentialGrant
	for rows.Next() {
		g, err := scanGrantRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan grant row: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grant rows: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Scan helpers
// ---------------------------------------------------------------------------

func scanEnrollmentRow(row rowScanner) (domain.WorkerEnrollment, error) {
	var (
		id, createdByRaw            uuid.NullUUID
		tokenHash                   []byte
		provider, name, namePattern string
		version                     int
		permissionTemplate          string
		createdAt, expiresAt        time.Time
		note, revokedReason         string
		maxUses, uses               int
		revokedAt                   *time.Time
		revokedByRaw                uuid.NullUUID
	)
	if err := row.Scan(
		&id, &tokenHash, &provider, &name, &version, &namePattern, &permissionTemplate,
		&createdAt, &createdByRaw, &note,
		&expiresAt, &maxUses, &uses,
		&revokedAt, &revokedByRaw, &revokedReason,
	); err != nil {
		return domain.WorkerEnrollment{}, err
	}

	e := domain.WorkerEnrollment{
		ID:                 domain.WorkerEnrollmentID(id.UUID),
		TokenHash:          tokenHash,
		Provider:           provider,
		Name:               name,
		Version:            version,
		WorkerNamePattern:  namePattern,
		PermissionTemplate: permissionTemplate,
		CreatedAt:          createdAt,
		Note:               note,
		ExpiresAt:          expiresAt,
		MaxUses:            maxUses,
		Uses:               uses,
		RevokedAt:          revokedAt,
		RevokedReason:      revokedReason,
	}
	if createdByRaw.Valid {
		u := domain.UserID(createdByRaw.UUID)
		e.CreatedByUserID = &u
	}
	if revokedByRaw.Valid {
		u := domain.UserID(revokedByRaw.UUID)
		e.RevokedByUserID = &u
	}
	return e, nil
}

func scanGrantRow(row rowScanner) (domain.WorkerCredentialGrant, error) {
	var (
		id, enrollmentID uuid.UUID
		userNKeyPublic   string
		workerName       string
		workerInstanceID string
		workerVersion    string
		clientHost       *string
		issuedAt         time.Time
		expiresAt        time.Time
		lastSeenAt       *time.Time
		revokedAt        *time.Time
		revokedByRaw     uuid.NullUUID
		revokedReason    string
	)
	if err := row.Scan(
		&id, &enrollmentID,
		&userNKeyPublic, &workerName, &workerInstanceID, &workerVersion,
		&clientHost,
		&issuedAt, &expiresAt, &lastSeenAt,
		&revokedAt, &revokedByRaw, &revokedReason,
	); err != nil {
		return domain.WorkerCredentialGrant{}, err
	}

	g := domain.WorkerCredentialGrant{
		ID:               domain.WorkerCredentialGrantID(id),
		EnrollmentID:     domain.WorkerEnrollmentID(enrollmentID),
		UserNKeyPublic:   userNKeyPublic,
		WorkerName:       workerName,
		WorkerInstanceID: workerInstanceID,
		WorkerVersion:    workerVersion,
		IssuedAt:         issuedAt,
		ExpiresAt:        expiresAt,
		LastSeenAt:       lastSeenAt,
		RevokedAt:        revokedAt,
		RevokedReason:    revokedReason,
	}
	if clientHost != nil {
		g.ClientHost = *clientHost
	}
	if revokedByRaw.Valid {
		u := domain.UserID(revokedByRaw.UUID)
		g.RevokedByUserID = &u
	}
	return g, nil
}
