package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FollowRepo implements port.FollowRepo over the follows table (migration 32).
type FollowRepo struct {
	pool *pgxpool.Pool
}

func NewFollowRepo(pool *pgxpool.Pool) *FollowRepo { return &FollowRepo{pool: pool} }

func (r *FollowRepo) Follow(ctx context.Context, follower, followee domain.UserID) error {
	if follower == followee {
		return fmt.Errorf("cannot follow yourself")
	}
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO follows (follower_id, followee_id, status) VALUES ($1, $2, 'accepted')
		 ON CONFLICT (follower_id, followee_id) DO NOTHING`,
		follower.UUID(), followee.UUID(),
	)
	if err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	return nil
}

func (r *FollowRepo) Unfollow(ctx context.Context, follower, followee domain.UserID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx, `DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`,
		follower.UUID(), followee.UUID())
	if err != nil {
		return fmt.Errorf("unfollow: %w", err)
	}
	return nil
}

func (r *FollowRepo) IsFollowing(ctx context.Context, follower, followee domain.UserID) (bool, error) {
	db := dbtx(ctx, r.pool)
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND followee_id=$2 AND status='accepted')`,
		follower.UUID(), followee.UUID()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is-following: %w", err)
	}
	return exists, nil
}

func (r *FollowRepo) ListFollowers(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.UserID, error) {
	return r.listEdge(ctx, `SELECT follower_id FROM follows WHERE followee_id=$1 AND status='accepted' ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
}

func (r *FollowRepo) ListFollowing(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.UserID, error) {
	return r.listEdge(ctx, `SELECT followee_id FROM follows WHERE follower_id=$1 AND status='accepted' ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
}

func (r *FollowRepo) listEdge(ctx context.Context, q string, userID domain.UserID, limit, offset int) ([]domain.UserID, error) {
	if limit <= 0 {
		limit = 100
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx, q, userID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list edge: %w", err)
	}
	defer rows.Close()
	var out []domain.UserID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, domain.UserID(id))
	}
	return out, rows.Err()
}

func (r *FollowRepo) Counts(ctx context.Context, userID domain.UserID) (domain.FollowCounts, error) {
	db := dbtx(ctx, r.pool)
	var c domain.FollowCounts
	err := db.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM follows WHERE followee_id=$1 AND status='accepted'),
		   (SELECT count(*) FROM follows WHERE follower_id=$1 AND status='accepted')`,
		userID.UUID()).Scan(&c.Followers, &c.Following)
	if err != nil {
		return domain.FollowCounts{}, fmt.Errorf("follow counts: %w", err)
	}
	return c, nil
}
