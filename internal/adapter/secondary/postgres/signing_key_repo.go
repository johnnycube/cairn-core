package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// SigningKeyRepo implements port.SigningKeyRepo on the
// instance_signing_keys table from migration 14.
//
// At most one key per purpose is active at a time, enforced by the
// `instance_signing_keys_one_active_per_purpose` partial unique index.
// Rotation is a two-step dance:
//
//  1. Operator runs the rotation use case which calls DeactivateAll
//     (marks all current rows inactive) and immediately CreateKey
//     with active=true.
//  2. NATS-server config is updated to trust both old and new account
//     public keys for an overlap window (typically a worker JWT TTL +
//     buffer). After the overlap, the old row is left as audit history.
type SigningKeyRepo struct {
	pool *pgxpool.Pool
}

func NewSigningKeyRepo(pool *pgxpool.Pool) *SigningKeyRepo {
	return &SigningKeyRepo{pool: pool}
}

const signingKeyColumns = `
	id, purpose, public_key, seed_encrypted, active,
	created_at, created_by_user_id, deactivated_at
`

// GetActive returns the one active key for the purpose. Returns
// domain.ErrNotFound if no key exists yet — the caller (typically the
// CredentialIssuer at first use) is expected to bootstrap one in that
// case.
func (r *SigningKeyRepo) GetActive(
	ctx context.Context,
	purpose string,
) (domain.SigningKey, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx,
		`SELECT `+signingKeyColumns+`
		   FROM instance_signing_keys
		  WHERE purpose = $1
		    AND active = true
		  LIMIT 1`,
		purpose,
	)
	k, err := scanSigningKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SigningKey{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SigningKey{}, fmt.Errorf("get active signing key (%s): %w", purpose, err)
	}
	return k, nil
}

// CreateKey inserts a new signing key. If `active=true` the caller must
// first DeactivateAll for the purpose to satisfy the partial unique
// index — otherwise insert fails with 23505.
func (r *SigningKeyRepo) CreateKey(
	ctx context.Context,
	k domain.SigningKey,
) (domain.SigningKey, error) {
	db := dbtx(ctx, r.pool)
	var createdBy any
	if k.CreatedByUserID != nil {
		createdBy = k.CreatedByUserID.UUID()
	}
	id := k.ID.UUID()
	if id == uuid.Nil {
		newID, err := uuid.NewV7()
		if err != nil {
			id = uuid.New()
		} else {
			id = newID
		}
	}
	row := db.QueryRow(ctx,
		`INSERT INTO instance_signing_keys (
		    id, purpose, public_key, seed_encrypted, active,
		    created_at, created_by_user_id
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+signingKeyColumns,
		id, k.Purpose, k.PublicKey, k.SeedEncrypted, k.Active,
		k.CreatedAt, createdBy,
	)
	created, err := scanSigningKeyRow(row)
	if err != nil {
		return domain.SigningKey{}, fmt.Errorf("insert signing key: %w", err)
	}
	return created, nil
}

// DeactivateAll for the purpose. Called inside the same Tx as a new
// CreateKey to perform an atomic rotation.
func (r *SigningKeyRepo) DeactivateAll(
	ctx context.Context,
	purpose string,
	at time.Time,
) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE instance_signing_keys
		    SET active = false,
		        deactivated_at = COALESCE(deactivated_at, $2)
		  WHERE purpose = $1
		    AND active = true`,
		purpose, at,
	)
	if err != nil {
		return fmt.Errorf("deactivate signing keys (%s): %w", purpose, err)
	}
	return nil
}

func scanSigningKeyRow(row rowScanner) (domain.SigningKey, error) {
	var (
		id            uuid.UUID
		purpose       string
		publicKey     string
		seedEncrypted []byte
		active        bool
		createdAt     time.Time
		createdByRaw  uuid.NullUUID
		deactivatedAt *time.Time
	)
	if err := row.Scan(
		&id, &purpose, &publicKey, &seedEncrypted, &active,
		&createdAt, &createdByRaw, &deactivatedAt,
	); err != nil {
		return domain.SigningKey{}, err
	}
	k := domain.SigningKey{
		ID:            domain.SigningKeyID(id),
		Purpose:       purpose,
		PublicKey:     publicKey,
		SeedEncrypted: seedEncrypted,
		Active:        active,
		CreatedAt:     createdAt,
		DeactivatedAt: deactivatedAt,
	}
	if createdByRaw.Valid {
		u := domain.UserID(createdByRaw.UUID)
		k.CreatedByUserID = &u
	}
	return k, nil
}
