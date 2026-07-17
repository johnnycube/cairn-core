package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ClubRepo implements port.ClubRepo over clubs + club_members (migration 38).
type ClubRepo struct{ pool *pgxpool.Pool }

func NewClubRepo(pool *pgxpool.Pool) *ClubRepo { return &ClubRepo{pool: pool} }

func scanClub(row rowScanner) (domain.Club, error) {
	var (
		c     domain.Club
		id    uuid.UUID
		owner uuid.UUID
	)
	if err := row.Scan(&id, &c.Slug, &c.Name, &c.Description, &owner, &c.IsPublic, &c.CreatedAt); err != nil {
		return domain.Club{}, err
	}
	c.ID = domain.ClubID(id)
	c.OwnerID = domain.UserID(owner)
	return c, nil
}

const clubColumns = `id, slug, name, description, owner_id, is_public, created_at`

func (r *ClubRepo) Create(ctx context.Context, c domain.Club) (domain.ClubID, error) {
	db := dbtx(ctx, r.pool)
	var id uuid.UUID
	err := db.QueryRow(ctx,
		`INSERT INTO clubs (slug, name, description, owner_id, is_public)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		c.Slug, c.Name, c.Description, c.OwnerID.UUID(), c.IsPublic).Scan(&id)
	if err != nil {
		return domain.ClubID{}, fmt.Errorf("create club: %w", err)
	}
	// The owner is automatically a member with the owner role.
	if _, err := db.Exec(ctx,
		`INSERT INTO club_members (club_id, user_id, role) VALUES ($1,$2,'owner')`,
		id, c.OwnerID.UUID()); err != nil {
		return domain.ClubID{}, fmt.Errorf("seed owner membership: %w", err)
	}
	return domain.ClubID(id), nil
}

func (r *ClubRepo) GetBySlug(ctx context.Context, slug string) (domain.Club, error) {
	db := dbtx(ctx, r.pool)
	row := db.QueryRow(ctx, `SELECT `+clubColumns+` FROM clubs WHERE slug = $1`, slug)
	c, err := scanClub(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Club{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Club{}, fmt.Errorf("get club by slug: %w", err)
	}
	return c, nil
}

func (r *ClubRepo) List(ctx context.Context, viewer domain.UserID, limit, offset int) ([]domain.Club, error) {
	if limit <= 0 {
		limit = 50
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+clubColumns+` FROM clubs c
		   WHERE c.is_public = true
		      OR EXISTS (SELECT 1 FROM club_members m WHERE m.club_id = c.id AND m.user_id = $1)
		   ORDER BY c.created_at DESC LIMIT $2 OFFSET $3`,
		viewer.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list clubs: %w", err)
	}
	defer rows.Close()
	return collectClubs(rows)
}

func (r *ClubRepo) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.Club, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+clubColumns+` FROM clubs c
		   JOIN club_members m ON m.club_id = c.id AND m.user_id = $1
		   ORDER BY c.name ASC`, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list user clubs: %w", err)
	}
	defer rows.Close()
	return collectClubs(rows)
}

func collectClubs(rows pgx.Rows) ([]domain.Club, error) {
	var out []domain.Club
	for rows.Next() {
		c, err := scanClub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ClubRepo) AddMember(ctx context.Context, clubID domain.ClubID, userID domain.UserID, role domain.ClubMemberRole) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO club_members (club_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (club_id, user_id) DO NOTHING`,
		clubID.UUID(), userID.UUID(), string(role))
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

func (r *ClubRepo) RemoveMember(ctx context.Context, clubID domain.ClubID, userID domain.UserID) error {
	db := dbtx(ctx, r.pool)
	// The owner cannot leave (would orphan the club); guard in the use case.
	_, err := db.Exec(ctx,
		`DELETE FROM club_members WHERE club_id=$1 AND user_id=$2 AND role <> 'owner'`,
		clubID.UUID(), userID.UUID())
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

func (r *ClubRepo) ListMembers(ctx context.Context, clubID domain.ClubID, limit, offset int) ([]domain.ClubMember, error) {
	if limit <= 0 {
		limit = 100
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT club_id, user_id, role, joined_at FROM club_members
		   WHERE club_id=$1 ORDER BY joined_at ASC LIMIT $2 OFFSET $3`,
		clubID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []domain.ClubMember
	for rows.Next() {
		var m domain.ClubMember
		var cid, uid uuid.UUID
		var role string
		if err := rows.Scan(&cid, &uid, &role, &m.JoinedAt); err != nil {
			return nil, err
		}
		m.ClubID = domain.ClubID(cid)
		m.UserID = domain.UserID(uid)
		m.Role = domain.ClubMemberRole(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ClubRepo) MemberRole(ctx context.Context, clubID domain.ClubID, userID domain.UserID) (domain.ClubMemberRole, bool, error) {
	db := dbtx(ctx, r.pool)
	var role string
	err := db.QueryRow(ctx,
		`SELECT role FROM club_members WHERE club_id=$1 AND user_id=$2`,
		clubID.UUID(), userID.UUID()).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("member role: %w", err)
	}
	return domain.ClubMemberRole(role), true, nil
}

func (r *ClubRepo) CountMembers(ctx context.Context, clubID domain.ClubID) (int, error) {
	db := dbtx(ctx, r.pool)
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM club_members WHERE club_id=$1`, clubID.UUID()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count members: %w", err)
	}
	return n, nil
}

func (r *ClubRepo) ListClubFeed(ctx context.Context, clubID domain.ClubID, limit, offset int) ([]domain.Activity, error) {
	if limit <= 0 {
		limit = 30
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT `+activityColumns+` FROM activities a
		   WHERE a.deleted_at IS NULL AND a.privacy <> 'private' AND a.hidden_by_admin = false
		     AND a.user_id IN (SELECT user_id FROM club_members WHERE club_id = $1)
		   ORDER BY a.start_time DESC, a.id DESC LIMIT $2 OFFSET $3`,
		clubID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("club feed: %w", err)
	}
	defer rows.Close()
	var out []domain.Activity
	for rows.Next() {
		a, err := scanActivityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
