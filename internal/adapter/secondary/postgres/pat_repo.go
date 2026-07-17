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

// PATRepo implements port.PATRepo on the personal_access_tokens table (migration 2).
type PATRepo struct {
	pool *pgxpool.Pool
}

func NewPATRepo(pool *pgxpool.Pool) *PATRepo {
	return &PATRepo{pool: pool}
}

const patCols = `id, user_id, name, token_prefix, scopes, created_at, expires_at, revoked_at, last_used_at`

func (r *PATRepo) Create(ctx context.Context, p domain.PersonalAccessToken, tokenHash []byte) (domain.PATID, error) {
	scopes := p.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO personal_access_tokens (user_id, name, token_hash, token_prefix, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		p.UserID.UUID(), p.Name, tokenHash, p.TokenPrefix, scopes, p.ExpiresAt).Scan(&id)
	if err != nil {
		return domain.PATID{}, fmt.Errorf("create pat: %w", err)
	}
	return domain.PATID(id), nil
}

func (r *PATRepo) FindByTokenHash(ctx context.Context, tokenHash []byte) (domain.PersonalAccessToken, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+patCols+` FROM personal_access_tokens WHERE token_hash = $1`, tokenHash)
	p, err := scanPAT(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PersonalAccessToken{}, fmt.Errorf("pat: %w", domain.ErrNotFound)
	}
	return p, err
}

func (r *PATRepo) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.PersonalAccessToken, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+patCols+` FROM personal_access_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list pats: %w", err)
	}
	defer rows.Close()
	var out []domain.PersonalAccessToken
	for rows.Next() {
		p, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *PATRepo) TouchLastUsed(ctx context.Context, id domain.PATID) error {
	_, err := r.pool.Exec(ctx, `UPDATE personal_access_tokens SET last_used_at = now() WHERE id = $1`, id.UUID())
	return err
}

func (r *PATRepo) Revoke(ctx context.Context, userID domain.UserID, id domain.PATID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE personal_access_tokens SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		id.UUID(), userID.UUID())
	if err != nil {
		return fmt.Errorf("revoke pat: %w", err)
	}
	return nil
}

func scanPAT(row pgx.Row) (domain.PersonalAccessToken, error) {
	var (
		p          domain.PersonalAccessToken
		id, uid    uuid.UUID
		expiresAt  *time.Time
		revokedAt  *time.Time
		lastUsedAt *time.Time
	)
	if err := row.Scan(&id, &uid, &p.Name, &p.TokenPrefix, &p.Scopes, &p.CreatedAt, &expiresAt, &revokedAt, &lastUsedAt); err != nil {
		return domain.PersonalAccessToken{}, err
	}
	p.ID = domain.PATID(id)
	p.UserID = domain.UserID(uid)
	p.ExpiresAt = expiresAt
	p.RevokedAt = revokedAt
	p.LastUsedAt = lastUsedAt
	return p, nil
}
