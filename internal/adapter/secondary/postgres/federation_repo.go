package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FederationActorRepo caches remote ActivityPub actors (federation_actors).
type FederationActorRepo struct{ pool *pgxpool.Pool }

func NewFederationActorRepo(pool *pgxpool.Pool) *FederationActorRepo {
	return &FederationActorRepo{pool: pool}
}

func (r *FederationActorRepo) Upsert(ctx context.Context, a domain.FederatedActor) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO federation_actors
		   (actor_id, inbox, shared_inbox, public_key_pem, preferred_username, domain, fetched_at)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (actor_id) DO UPDATE SET
		   inbox=EXCLUDED.inbox, shared_inbox=EXCLUDED.shared_inbox,
		   public_key_pem=EXCLUDED.public_key_pem,
		   preferred_username=EXCLUDED.preferred_username,
		   domain=EXCLUDED.domain, fetched_at=now()`,
		a.ActorID, a.Inbox, nullStr(a.SharedInbox), a.PublicKeyPEM,
		nullStr(a.PreferredUsername), nullStr(a.Domain),
	)
	if err != nil {
		return fmt.Errorf("upsert federated actor: %w", err)
	}
	return nil
}

func (r *FederationActorRepo) Get(ctx context.Context, actorID string) (domain.FederatedActor, error) {
	db := dbtx(ctx, r.pool)
	var a domain.FederatedActor
	var shared, uname, dom *string
	err := db.QueryRow(ctx,
		`SELECT actor_id, inbox, shared_inbox, public_key_pem, preferred_username, domain, fetched_at
		   FROM federation_actors WHERE actor_id=$1`, actorID,
	).Scan(&a.ActorID, &a.Inbox, &shared, &a.PublicKeyPEM, &uname, &dom, &a.FetchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FederatedActor{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.FederatedActor{}, fmt.Errorf("get federated actor: %w", err)
	}
	a.SharedInbox, a.PreferredUsername, a.Domain = deref(shared), deref(uname), deref(dom)
	return a, nil
}

// FederationFollowRepo persists cross-instance follow edges (federation_follows).
type FederationFollowRepo struct{ pool *pgxpool.Pool }

func NewFederationFollowRepo(pool *pgxpool.Pool) *FederationFollowRepo {
	return &FederationFollowRepo{pool: pool}
}

func (r *FederationFollowRepo) Upsert(ctx context.Context, f domain.FederationFollow) error {
	db := dbtx(ctx, r.pool)
	id := f.ID
	if id == (domain.FederationFollowID{}) {
		id = domain.FederationFollowID(uuid.New())
	}
	_, err := db.Exec(ctx,
		`INSERT INTO federation_follows
		   (id, local_user_id, remote_actor_id, direction, status, follow_activity_id)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (local_user_id, remote_actor_id, direction) DO UPDATE SET
		   status=EXCLUDED.status, follow_activity_id=EXCLUDED.follow_activity_id`,
		id.UUID(), f.LocalUserID.UUID(), f.RemoteActorID,
		string(f.Direction), string(f.Status), nullStr(f.FollowActivityID),
	)
	if err != nil {
		return fmt.Errorf("upsert federation follow: %w", err)
	}
	return nil
}

func (r *FederationFollowRepo) ListInboundFollowerInboxes(ctx context.Context, userID domain.UserID) ([]string, error) {
	db := dbtx(ctx, r.pool)
	// Join accepted inbound followers to their cached actor record, preferring
	// the shared inbox when present (fewer round-trips on a busy instance).
	rows, err := db.Query(ctx,
		`SELECT DISTINCT COALESCE(NULLIF(a.shared_inbox, ''), a.inbox) AS inbox
		   FROM federation_follows f
		   JOIN federation_actors a ON a.actor_id = f.remote_actor_id
		  WHERE f.local_user_id = $1
		    AND f.direction = 'inbound'
		    AND f.status = 'accepted'
		    AND COALESCE(NULLIF(a.shared_inbox, ''), a.inbox) <> ''`,
		userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list inbound follower inboxes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var inbox string
		if err := rows.Scan(&inbox); err != nil {
			return nil, fmt.Errorf("scan inbox: %w", err)
		}
		out = append(out, inbox)
	}
	return out, rows.Err()
}

func (r *FederationFollowRepo) Delete(ctx context.Context, userID domain.UserID, remoteActorID string, dir domain.FederationFollowDirection) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM federation_follows
		   WHERE local_user_id=$1 AND remote_actor_id=$2 AND direction=$3`,
		userID.UUID(), remoteActorID, string(dir))
	if err != nil {
		return fmt.Errorf("delete federation follow: %w", err)
	}
	return nil
}

// FederationBlockRepo is the instance defederation blocklist
// (federation_blocked_domains).
type FederationBlockRepo struct{ pool *pgxpool.Pool }

func NewFederationBlockRepo(pool *pgxpool.Pool) *FederationBlockRepo {
	return &FederationBlockRepo{pool: pool}
}

func normDomain(d string) string { return strings.ToLower(strings.TrimSpace(d)) }

func (r *FederationBlockRepo) Block(ctx context.Context, domain, reason string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`INSERT INTO federation_blocked_domains (domain, reason) VALUES ($1,$2)
		 ON CONFLICT (domain) DO UPDATE SET reason = EXCLUDED.reason`,
		normDomain(domain), reason)
	if err != nil {
		return fmt.Errorf("block domain: %w", err)
	}
	return nil
}

func (r *FederationBlockRepo) Unblock(ctx context.Context, domain string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx, `DELETE FROM federation_blocked_domains WHERE domain=$1`, normDomain(domain))
	if err != nil {
		return fmt.Errorf("unblock domain: %w", err)
	}
	return nil
}

func (r *FederationBlockRepo) IsBlocked(ctx context.Context, domain string) (bool, error) {
	d := normDomain(domain)
	if d == "" {
		return false, nil
	}
	db := dbtx(ctx, r.pool)
	var blocked bool
	if err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM federation_blocked_domains WHERE domain=$1)`, d).Scan(&blocked); err != nil {
		return false, fmt.Errorf("is domain blocked: %w", err)
	}
	return blocked, nil
}

func (r *FederationBlockRepo) List(ctx context.Context) ([]domain.BlockedDomain, error) {
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT domain, reason, created_at FROM federation_blocked_domains ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list blocked domains: %w", err)
	}
	defer rows.Close()
	var out []domain.BlockedDomain
	for rows.Next() {
		var b domain.BlockedDomain
		if err := rows.Scan(&b.Domain, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InboxDedupRepo de-duplicates inbound activities (federation_inbox_seen).
type InboxDedupRepo struct{ pool *pgxpool.Pool }

func NewInboxDedupRepo(pool *pgxpool.Pool) *InboxDedupRepo { return &InboxDedupRepo{pool: pool} }

func (r *InboxDedupRepo) SeenOrMark(ctx context.Context, activityID string) (bool, error) {
	db := dbtx(ctx, r.pool)
	// INSERT ... ON CONFLICT DO NOTHING RETURNING: a returned row means we just
	// inserted (first sight); no row means it was already present (duplicate).
	var inserted string
	err := db.QueryRow(ctx,
		`INSERT INTO federation_inbox_seen (activity_id) VALUES ($1)
		 ON CONFLICT (activity_id) DO NOTHING RETURNING activity_id`, activityID,
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil // already seen
	}
	if err != nil {
		return false, fmt.Errorf("inbox dedup: %w", err)
	}
	return false, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// FederationFeedRepo stores inbound remote activities (federation_feed_items).
type FederationFeedRepo struct{ pool *pgxpool.Pool }

func NewFederationFeedRepo(pool *pgxpool.Pool) *FederationFeedRepo {
	return &FederationFeedRepo{pool: pool}
}

func (r *FederationFeedRepo) Insert(ctx context.Context, it domain.FederatedFeedItem) error {
	db := dbtx(ctx, r.pool)
	id := it.ID
	if id == (domain.FederationFeedItemID{}) {
		id = domain.FederationFeedItemID(uuid.New())
	}
	_, err := db.Exec(ctx,
		`INSERT INTO federation_feed_items
		   (id, recipient_user_id, actor_id, activity_ap_id, published,
		    name, summary, url, image_url, sport, distance_m, duration_s, elevation_m)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 ON CONFLICT (recipient_user_id, activity_ap_id) DO NOTHING`,
		id.UUID(), it.RecipientID.UUID(), it.ActorID, it.ActivityAPID, it.Published.UTC(),
		it.Name, it.Summary, it.URL, it.ImageURL, it.Sport, it.DistanceM, it.DurationS, it.ElevationM,
	)
	if err != nil {
		return fmt.Errorf("insert federation feed item: %w", err)
	}
	return nil
}

func (r *FederationFeedRepo) ListForUser(ctx context.Context, userID domain.UserID, limit, offset int) ([]domain.FederatedFeedItem, error) {
	if limit <= 0 {
		limit = 30
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT id, actor_id, activity_ap_id, published, name, summary, url, image_url,
		        sport, distance_m, duration_s, elevation_m
		   FROM federation_feed_items
		  WHERE recipient_user_id = $1
		  ORDER BY published DESC
		  LIMIT $2 OFFSET $3`,
		userID.UUID(), limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list federation feed: %w", err)
	}
	defer rows.Close()
	var out []domain.FederatedFeedItem
	for rows.Next() {
		var it domain.FederatedFeedItem
		var id uuid.UUID
		if err := rows.Scan(&id, &it.ActorID, &it.ActivityAPID, &it.Published, &it.Name,
			&it.Summary, &it.URL, &it.ImageURL, &it.Sport, &it.DistanceM, &it.DurationS, &it.ElevationM); err != nil {
			return nil, fmt.Errorf("scan federation feed item: %w", err)
		}
		it.ID = domain.FederationFeedItemID(id)
		it.RecipientID = userID
		out = append(out, it)
	}
	return out, rows.Err()
}

// FederationDeliveryRepo is the durable outbound delivery queue
// (federation_deliveries).
type FederationDeliveryRepo struct{ pool *pgxpool.Pool }

func NewFederationDeliveryRepo(pool *pgxpool.Pool) *FederationDeliveryRepo {
	return &FederationDeliveryRepo{pool: pool}
}

func (r *FederationDeliveryRepo) Enqueue(ctx context.Context, d domain.FederationDelivery) error {
	db := dbtx(ctx, r.pool)
	id := d.ID
	if id == (domain.FederationDeliveryID{}) {
		id = domain.FederationDeliveryID(uuid.New())
	}
	maxAttempts := d.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	_, err := db.Exec(ctx,
		`INSERT INTO federation_deliveries
		   (id, from_user_id, actor_id, inbox_url, body, activity_ap_id, max_attempts)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (from_user_id, inbox_url, activity_ap_id) DO NOTHING`,
		id.UUID(), d.FromUserID.UUID(), d.ActorID, d.InboxURL, d.Body, d.ActivityAPID, maxAttempts,
	)
	if err != nil {
		return fmt.Errorf("enqueue federation delivery: %w", err)
	}
	return nil
}

// ListDue atomically CLAIMS up to `limit` due deliveries: it leases each
// selected row by pushing next_attempt_at forward (5 min) under FOR UPDATE SKIP
// LOCKED, so a concurrent tick or replica won't re-select the same rows and
// double-POST. The caller overwrites the lease via MarkDelivered/Reschedule/
// MarkDead; if the process dies mid-delivery, the lease expires and the row is
// retried.
func (r *FederationDeliveryRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]domain.FederationDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`UPDATE federation_deliveries SET next_attempt_at = $2, updated_at = now()
		  WHERE id IN (
		    SELECT id FROM federation_deliveries
		     WHERE status = 'pending' AND next_attempt_at <= $1
		     ORDER BY next_attempt_at
		     LIMIT $3
		     FOR UPDATE SKIP LOCKED
		  )
		  RETURNING id, from_user_id, actor_id, inbox_url, body, activity_ap_id,
		            status, attempts, max_attempts, next_attempt_at, last_error`,
		now.UTC(), now.UTC().Add(5*time.Minute), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim due federation deliveries: %w", err)
	}
	defer rows.Close()
	var out []domain.FederationDelivery
	for rows.Next() {
		var d domain.FederationDelivery
		var id, fromUser uuid.UUID
		var status string
		if err := rows.Scan(&id, &fromUser, &d.ActorID, &d.InboxURL, &d.Body, &d.ActivityAPID,
			&status, &d.Attempts, &d.MaxAttempts, &d.NextAttemptAt, &d.LastError); err != nil {
			return nil, fmt.Errorf("scan federation delivery: %w", err)
		}
		d.ID = domain.FederationDeliveryID(id)
		d.FromUserID = domain.UserID(fromUser)
		d.Status = domain.FederationDeliveryStatus(status)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *FederationDeliveryRepo) MarkDelivered(ctx context.Context, id domain.FederationDeliveryID) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE federation_deliveries
		    SET status='delivered', delivered_at=now(), updated_at=now(), last_error=''
		  WHERE id=$1`, id.UUID())
	if err != nil {
		return fmt.Errorf("mark federation delivery delivered: %w", err)
	}
	return nil
}

func (r *FederationDeliveryRepo) Reschedule(ctx context.Context, id domain.FederationDeliveryID, attempts int, nextAt time.Time, lastErr string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE federation_deliveries
		    SET attempts=$2, next_attempt_at=$3, last_error=$4, updated_at=now()
		  WHERE id=$1`, id.UUID(), attempts, nextAt.UTC(), lastErr)
	if err != nil {
		return fmt.Errorf("reschedule federation delivery: %w", err)
	}
	return nil
}

func (r *FederationDeliveryRepo) MarkDead(ctx context.Context, id domain.FederationDeliveryID, lastErr string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE federation_deliveries
		    SET status='dead', attempts=attempts+1, last_error=$2, updated_at=now()
		  WHERE id=$1`, id.UUID(), lastErr)
	if err != nil {
		return fmt.Errorf("mark federation delivery dead: %w", err)
	}
	return nil
}

func (r *FederationFeedRepo) DeleteItem(ctx context.Context, recipient domain.UserID, actorID, activityAPID string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM federation_feed_items
		   WHERE recipient_user_id=$1 AND actor_id=$2 AND activity_ap_id=$3`,
		recipient.UUID(), actorID, activityAPID)
	if err != nil {
		return fmt.Errorf("delete federation feed item: %w", err)
	}
	return nil
}

func (r *FederationFeedRepo) DeleteAllFromActor(ctx context.Context, recipient domain.UserID, actorID string) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`DELETE FROM federation_feed_items
		   WHERE recipient_user_id=$1 AND actor_id=$2`,
		recipient.UUID(), actorID)
	if err != nil {
		return fmt.Errorf("delete federation feed items from actor: %w", err)
	}
	return nil
}

func (r *FederationFollowRepo) Exists(ctx context.Context, userID domain.UserID, remoteActorID string, dir domain.FederationFollowDirection) (bool, error) {
	db := dbtx(ctx, r.pool)
	var exists bool
	if err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM federation_follows
		   WHERE local_user_id=$1 AND remote_actor_id=$2 AND direction=$3)`,
		userID.UUID(), remoteActorID, string(dir)).Scan(&exists); err != nil {
		return false, fmt.Errorf("follow exists: %w", err)
	}
	return exists, nil
}

func (r *FederationFollowRepo) MarkAccepted(ctx context.Context, userID domain.UserID, remoteActorID string, dir domain.FederationFollowDirection) error {
	db := dbtx(ctx, r.pool)
	_, err := db.Exec(ctx,
		`UPDATE federation_follows SET status='accepted'
		   WHERE local_user_id=$1 AND remote_actor_id=$2 AND direction=$3`,
		userID.UUID(), remoteActorID, string(dir))
	if err != nil {
		return fmt.Errorf("mark follow accepted: %w", err)
	}
	return nil
}
