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

// PasskeyRepo implements port.PasskeyRepo on the webauthn_credentials table.
type PasskeyRepo struct {
	pool *pgxpool.Pool
}

func NewPasskeyRepo(pool *pgxpool.Pool) *PasskeyRepo {
	return &PasskeyRepo{pool: pool}
}

func (r *PasskeyRepo) Create(ctx context.Context, p domain.Passkey) (domain.PasskeyID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO webauthn_credentials (user_id, credential_id, credential, name)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		p.UserID.UUID(), p.CredentialID, p.CredentialJSON, p.Name).Scan(&id)
	if err != nil {
		return domain.PasskeyID{}, fmt.Errorf("create passkey: %w", err)
	}
	return domain.PasskeyID(id), nil
}

func (r *PasskeyRepo) ListByUser(ctx context.Context, userID domain.UserID) ([]domain.Passkey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, credential_id, credential, name, created_at, last_used_at
		  FROM webauthn_credentials
		 WHERE user_id = $1
		 ORDER BY created_at DESC`, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var out []domain.Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *PasskeyRepo) GetByCredentialID(ctx context.Context, credentialID []byte) (domain.Passkey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, credential_id, credential, name, created_at, last_used_at
		  FROM webauthn_credentials
		 WHERE credential_id = $1`, credentialID)
	p, err := scanPasskey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Passkey{}, fmt.Errorf("passkey: %w", domain.ErrNotFound)
	}
	return p, err
}

func (r *PasskeyRepo) UpdateCredential(ctx context.Context, credentialID []byte, credentialJSON []byte) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE webauthn_credentials
		   SET credential = $2, last_used_at = now()
		 WHERE credential_id = $1`, credentialID, credentialJSON)
	if err != nil {
		return fmt.Errorf("update passkey credential: %w", err)
	}
	return nil
}

func (r *PasskeyRepo) Rename(ctx context.Context, userID domain.UserID, id domain.PasskeyID, name string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE webauthn_credentials SET name = $3 WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID(), name)
	if err != nil {
		return fmt.Errorf("rename passkey: %w", err)
	}
	return nil
}

func (r *PasskeyRepo) Delete(ctx context.Context, userID domain.UserID, id domain.PasskeyID) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID())
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	return nil
}

func scanPasskey(row pgx.Row) (domain.Passkey, error) {
	var (
		p          domain.Passkey
		id, uid    uuid.UUID
		lastUsedAt *time.Time
	)
	if err := row.Scan(&id, &uid, &p.CredentialID, &p.CredentialJSON, &p.Name, &p.CreatedAt, &lastUsedAt); err != nil {
		return domain.Passkey{}, err
	}
	p.ID = domain.PasskeyID(id)
	p.UserID = domain.UserID(uid)
	p.LastUsedAt = lastUsedAt
	return p, nil
}
