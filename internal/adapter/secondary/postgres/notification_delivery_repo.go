package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// NotificationDeliveryRepo implements port.NotificationDeliveryRepo over the
// notification_deliveries audit table (migration 10).
type NotificationDeliveryRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationDeliveryRepo(pool *pgxpool.Pool) *NotificationDeliveryRepo {
	return &NotificationDeliveryRepo{pool: pool}
}

func (r *NotificationDeliveryRepo) Record(ctx context.Context, d domain.NotificationDelivery) error {
	db := dbtx(ctx, r.pool)

	var webhookParam, emailParam, codeParam, errParam any
	if d.WebhookEndpoint != nil {
		webhookParam = d.WebhookEndpoint.UUID()
	}
	if d.EmailAddress != "" {
		emailParam = d.EmailAddress
	}
	if d.HTTPStatusCode != 0 {
		codeParam = int16(d.HTTPStatusCode)
	}
	if d.ErrorMessage != "" {
		errParam = d.ErrorMessage
	}
	attempt := d.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	var nextRetryParam any
	if d.NextRetryAt != nil {
		nextRetryParam = d.NextRetryAt.UTC()
	}

	_, err := db.Exec(ctx,
		`INSERT INTO notification_deliveries
		    (event_id, user_id, channel, webhook_endpoint_id, email_address,
		     status, http_status_code, error_message, attempt, attempted_at, next_retry_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, now()), $11)`,
		d.EventID.UUID(), d.UserID.UUID(), string(d.Channel), webhookParam, emailParam,
		string(d.Status), codeParam, errParam, attempt, nullableTime(d.AttemptedAt), nextRetryParam,
	)
	if err != nil {
		return fmt.Errorf("record notification delivery: %w", err)
	}
	return nil
}

// retryLease is how far ListDueRetries pushes a claimed row's next_retry_at
// into the future. It must exceed the time RetryDue needs to process one batch,
// so a crashed processor's row only re-runs after the lease (and a second
// replica never double-sends a row mid-flight). RetryDue overwrites it with the
// real backoff (or clears it on success) when it records the outcome.
const retryLease = 15 * time.Minute

// ListDueRetries atomically CLAIMS up to limit failed_retryable rows whose
// next_retry_at has elapsed: it leases each by pushing next_retry_at forward by
// retryLease and returns the pre-claim attempt. FOR UPDATE SKIP LOCKED + the
// forward lease mean concurrent server replicas never pick the same row, so a
// horizontally-scaled deploy can't double-send. Oldest-due first.
func (r *NotificationDeliveryRepo) ListDueRetries(ctx context.Context, now time.Time, limit int) ([]domain.NotificationDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`UPDATE notification_deliveries d
		    SET next_retry_at = $1::timestamptz + $3::interval
		   FROM (
		     SELECT id FROM notification_deliveries
		      WHERE status = 'failed_retryable'
		        AND next_retry_at IS NOT NULL
		        AND next_retry_at <= $1
		      ORDER BY next_retry_at
		      LIMIT $2
		      FOR UPDATE SKIP LOCKED
		   ) claimed
		  WHERE d.id = claimed.id
		  RETURNING d.id, d.event_id, d.user_id, d.channel, d.webhook_endpoint_id,
		            d.email_address, d.http_status_code, d.error_message, d.attempt`,
		now.UTC(), limit, retryLease,
	)
	if err != nil {
		return nil, fmt.Errorf("claim due retries: %w", err)
	}
	defer rows.Close()

	var out []domain.NotificationDelivery
	for rows.Next() {
		var (
			d                domain.NotificationDelivery
			id, eventID, uid uuid.UUID
			whID             *uuid.UUID
			email            *string
			code             *int16
			errMsg           *string
			channel          string
		)
		if err := rows.Scan(&id, &eventID, &uid, &channel, &whID, &email, &code, &errMsg, &d.Attempt); err != nil {
			return nil, fmt.Errorf("scan due retry: %w", err)
		}
		d.ID = domain.NotificationDeliveryID(id)
		d.EventID = domain.NotificationID(eventID)
		d.UserID = domain.UserID(uid)
		d.Channel = domain.NotificationChannel(channel)
		d.Status = domain.DeliveryStatusFailedRetryable
		if whID != nil {
			w := domain.WebhookEndpointID(*whID)
			d.WebhookEndpoint = &w
		}
		if email != nil {
			d.EmailAddress = *email
		}
		if code != nil {
			d.HTTPStatusCode = int(*code)
		}
		if errMsg != nil {
			d.ErrorMessage = *errMsg
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateOutcome rewrites one delivery row after a retry attempt.
func (r *NotificationDeliveryRepo) UpdateOutcome(
	ctx context.Context,
	id domain.NotificationDeliveryID,
	status domain.DeliveryStatus,
	attempt int,
	nextRetryAt *time.Time,
	httpStatusCode int,
	errorMessage string,
	attemptedAt time.Time,
) error {
	db := dbtx(ctx, r.pool)
	var codeParam, errParam, nextParam any
	if httpStatusCode != 0 {
		codeParam = int16(httpStatusCode)
	}
	if errorMessage != "" {
		errParam = errorMessage
	}
	if nextRetryAt != nil {
		nextParam = nextRetryAt.UTC()
	}
	_, err := db.Exec(ctx,
		`UPDATE notification_deliveries
		    SET status = $2, attempt = $3, next_retry_at = $4,
		        http_status_code = $5, error_message = $6, attempted_at = $7
		  WHERE id = $1`,
		id.UUID(), string(status), attempt, nextParam, codeParam, errParam, attemptedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("update delivery outcome: %w", err)
	}
	return nil
}

func (r *NotificationDeliveryRepo) ListForUser(ctx context.Context, userID domain.UserID, limit int) ([]domain.NotificationDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	db := dbtx(ctx, r.pool)
	rows, err := db.Query(ctx,
		`SELECT event_id, channel, webhook_endpoint_id, email_address,
		        status, http_status_code, error_message, attempt, attempted_at
		   FROM notification_deliveries
		  WHERE user_id = $1
		  ORDER BY attempted_at DESC
		  LIMIT $2`,
		userID.UUID(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()

	var out []domain.NotificationDelivery
	for rows.Next() {
		var (
			d       domain.NotificationDelivery
			eventID uuid.UUID
			whID    *uuid.UUID
			email   *string
			code    *int16
			errMsg  *string
			channel string
			status  string
		)
		if err := rows.Scan(&eventID, &channel, &whID, &email, &status, &code, &errMsg, &d.Attempt, &d.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		d.EventID = domain.NotificationID(eventID)
		d.UserID = userID
		d.Channel = domain.NotificationChannel(channel)
		d.Status = domain.DeliveryStatus(status)
		if whID != nil {
			id := domain.WebhookEndpointID(*whID)
			d.WebhookEndpoint = &id
		}
		if email != nil {
			d.EmailAddress = *email
		}
		if code != nil {
			d.HTTPStatusCode = int(*code)
		}
		if errMsg != nil {
			d.ErrorMessage = *errMsg
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
