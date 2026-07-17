package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// WebhookEndpointRepo implements port.WebhookEndpointRepo over the
// webhook_endpoints table (migration 10). The HMAC signing secret is stored
// encrypted with the instance master key, AAD-bound to the row id
// ("webhook_secret:<id>") so ciphertext can't be swapped between rows.
type WebhookEndpointRepo struct {
	pool    *pgxpool.Pool
	secrets *auth.SecretBox
}

func NewWebhookEndpointRepo(pool *pgxpool.Pool, secrets *auth.SecretBox) *WebhookEndpointRepo {
	return &WebhookEndpointRepo{pool: pool, secrets: secrets}
}

func webhookSecretAAD(id domain.WebhookEndpointID) []byte {
	return []byte("webhook_secret:" + id.String())
}

const webhookColumns = `id, user_id, name, url,
	signing_secret_rotated_at, event_types, min_severity, enabled,
	last_delivery_at, last_delivery_status_code, last_delivery_error,
	consecutive_failures, auto_disabled, auto_disabled_at, created_at, updated_at`

// scanWebhookRow scans the webhookColumns (no secret). withSecret callers add
// signing_secret_encrypted separately.
func scanWebhookRow(row pgx.Row) (domain.WebhookEndpoint, error) {
	var (
		e          domain.WebhookEndpoint
		id, userID uuid.UUID
		eventTypes []int32
		minSev     string
		lastAt     *time.Time
		lastCode   *int16
		lastErr    *string
		autoAt     *time.Time
	)
	if err := row.Scan(
		&id, &userID, &e.Name, &e.URL,
		&e.SigningSecretRotated, &eventTypes, &minSev, &e.Enabled,
		&lastAt, &lastCode, &lastErr,
		&e.ConsecutiveFailures, &e.AutoDisabled, &autoAt, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	e.ID = domain.WebhookEndpointID(id)
	e.UserID = domain.UserID(userID)
	e.MinSeverity = domain.NotificationSeverity(minSev)
	e.EventTypes = make([]domain.NotificationType, 0, len(eventTypes))
	for _, t := range eventTypes {
		e.EventTypes = append(e.EventTypes, domain.NotificationType(t))
	}
	e.LastDeliveryAt = lastAt
	if lastCode != nil {
		e.LastDeliveryStatusCode = int(*lastCode)
	}
	if lastErr != nil {
		e.LastDeliveryError = *lastErr
	}
	e.AutoDisabledAt = autoAt
	return e, nil
}

func eventTypesToInt32(types []domain.NotificationType) []int32 {
	out := make([]int32, 0, len(types))
	for _, t := range types {
		out = append(out, int32(t))
	}
	return out
}

func (r *WebhookEndpointRepo) Create(ctx context.Context, e domain.WebhookEndpoint) (domain.WebhookEndpoint, error) {
	db := dbtx(ctx, r.pool)
	id, err := uuid.NewV7()
	if err != nil {
		return domain.WebhookEndpoint{}, fmt.Errorf("generate webhook id: %w", err)
	}
	e.ID = domain.WebhookEndpointID(id)

	enc, err := r.secrets.Encrypt([]byte(e.SigningSecret), webhookSecretAAD(e.ID))
	if err != nil {
		return domain.WebhookEndpoint{}, fmt.Errorf("encrypt webhook secret: %w", err)
	}

	row := db.QueryRow(ctx,
		`INSERT INTO webhook_endpoints
		    (id, user_id, name, url, signing_secret_encrypted, event_types, min_severity, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+webhookColumns,
		id, e.UserID.UUID(), e.Name, e.URL, enc,
		eventTypesToInt32(e.EventTypes), string(e.MinSeverity), e.Enabled,
	)
	out, err := scanWebhookRow(row)
	if err != nil {
		return domain.WebhookEndpoint{}, fmt.Errorf("insert webhook endpoint: %w", err)
	}
	out.SigningSecret = e.SigningSecret // echo back so the caller can show it once
	return out, nil
}

func (r *WebhookEndpointRepo) Update(ctx context.Context, e domain.WebhookEndpoint) error {
	db := dbtx(ctx, r.pool)
	// Re-enabling clears the auto-disabled latch + failure counter so a fixed
	// endpoint starts fresh.
	tag, err := db.Exec(ctx,
		`UPDATE webhook_endpoints
		    SET name = $3, url = $4, event_types = $5, min_severity = $6, enabled = $7,
		        auto_disabled = CASE WHEN $7 THEN false ELSE auto_disabled END,
		        auto_disabled_at = CASE WHEN $7 THEN NULL ELSE auto_disabled_at END,
		        consecutive_failures = CASE WHEN $7 THEN 0 ELSE consecutive_failures END,
		        updated_at = now()
		  WHERE id = $1 AND user_id = $2`,
		e.ID.UUID(), e.UserID.UUID(), e.Name, e.URL,
		eventTypesToInt32(e.EventTypes), string(e.MinSeverity), e.Enabled,
	)
	if err != nil {
		return fmt.Errorf("update webhook endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookEndpointRepo) RotateSecret(ctx context.Context, id domain.WebhookEndpointID, userID domain.UserID, newSecret string) error {
	db := dbtx(ctx, r.pool)
	enc, err := r.secrets.Encrypt([]byte(newSecret), webhookSecretAAD(id))
	if err != nil {
		return fmt.Errorf("encrypt webhook secret: %w", err)
	}
	tag, err := db.Exec(ctx,
		`UPDATE webhook_endpoints
		    SET signing_secret_encrypted = $3, signing_secret_rotated_at = now(), updated_at = now()
		  WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID(), enc,
	)
	if err != nil {
		return fmt.Errorf("rotate webhook secret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookEndpointRepo) Delete(ctx context.Context, id domain.WebhookEndpointID, userID domain.UserID) error {
	db := dbtx(ctx, r.pool)
	tag, err := db.Exec(ctx,
		`DELETE FROM webhook_endpoints WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID(),
	)
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookEndpointRepo) GetByID(ctx context.Context, id domain.WebhookEndpointID, userID domain.UserID) (domain.WebhookEndpoint, error) {
	db := dbtx(ctx, r.pool)
	var enc []byte
	row := db.QueryRow(ctx,
		`SELECT `+webhookColumns+`, signing_secret_encrypted
		   FROM webhook_endpoints WHERE id = $1 AND user_id = $2`,
		id.UUID(), userID.UUID(),
	)
	e, enc, err := scanWebhookRowWithSecret(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WebhookEndpoint{}, domain.ErrNotFound
		}
		return domain.WebhookEndpoint{}, fmt.Errorf("get webhook endpoint: %w", err)
	}
	plain, err := r.secrets.Decrypt(enc, webhookSecretAAD(e.ID))
	if err != nil {
		return domain.WebhookEndpoint{}, fmt.Errorf("decrypt webhook secret: %w", err)
	}
	e.SigningSecret = string(plain)
	return e, nil
}

func (r *WebhookEndpointRepo) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.WebhookEndpoint, error) {
	return r.list(ctx, userID, false)
}

func (r *WebhookEndpointRepo) ListEnabledForUser(ctx context.Context, userID domain.UserID) ([]domain.WebhookEndpoint, error) {
	return r.list(ctx, userID, true)
}

func (r *WebhookEndpointRepo) list(ctx context.Context, userID domain.UserID, enabledOnly bool) ([]domain.WebhookEndpoint, error) {
	db := dbtx(ctx, r.pool)
	q := `SELECT ` + webhookColumns + `, signing_secret_encrypted
	        FROM webhook_endpoints WHERE user_id = $1`
	if enabledOnly {
		q += ` AND enabled = true AND auto_disabled = false`
	}
	q += ` ORDER BY created_at DESC`

	rows, err := db.Query(ctx, q, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("list webhook endpoints: %w", err)
	}
	defer rows.Close()

	var out []domain.WebhookEndpoint
	for rows.Next() {
		e, enc, err := scanWebhookRowWithSecret(rows)
		if err != nil {
			return nil, fmt.Errorf("scan webhook row: %w", err)
		}
		if enabledOnly {
			// Dispatch path needs the secret to sign.
			plain, derr := r.secrets.Decrypt(enc, webhookSecretAAD(e.ID))
			if derr != nil {
				// A row whose secret won't decrypt can't be signed — skip it
				// rather than abort the whole batch.
				continue
			}
			e.SigningSecret = string(plain)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *WebhookEndpointRepo) RecordDeliveryResult(ctx context.Context, id domain.WebhookEndpointID, statusCode int, deliveryErr string, at time.Time) error {
	db := dbtx(ctx, r.pool)
	success := deliveryErr == "" && statusCode >= 200 && statusCode < 300
	var codeParam any
	if statusCode != 0 {
		codeParam = int16(statusCode)
	}
	var errParam any
	if deliveryErr != "" {
		errParam = deliveryErr
	}
	// On success: reset failures, clear error. On failure: ++failures, and
	// auto-disable once the running count reaches the threshold.
	_, err := db.Exec(ctx,
		`UPDATE webhook_endpoints
		    SET last_delivery_at = $2,
		        last_delivery_status_code = $3,
		        last_delivery_error = $4,
		        consecutive_failures = CASE WHEN $5 THEN 0 ELSE consecutive_failures + 1 END,
		        auto_disabled = CASE WHEN $5 THEN auto_disabled
		                             WHEN consecutive_failures + 1 >= $6 THEN true
		                             ELSE auto_disabled END,
		        auto_disabled_at = CASE WHEN $5 THEN auto_disabled_at
		                                WHEN consecutive_failures + 1 >= $6 AND NOT auto_disabled THEN $2
		                                ELSE auto_disabled_at END,
		        updated_at = now()
		  WHERE id = $1`,
		id.UUID(), at.UTC(), codeParam, errParam, success, domain.WebhookMaxConsecutiveFailures,
	)
	if err != nil {
		return fmt.Errorf("record webhook delivery: %w", err)
	}
	return nil
}

// scanWebhookRowWithSecret scans webhookColumns + the trailing
// signing_secret_encrypted bytea.
func scanWebhookRowWithSecret(row pgx.Row) (domain.WebhookEndpoint, []byte, error) {
	var (
		e          domain.WebhookEndpoint
		id, userID uuid.UUID
		eventTypes []int32
		minSev     string
		lastAt     *time.Time
		lastCode   *int16
		lastErr    *string
		autoAt     *time.Time
		enc        []byte
	)
	if err := row.Scan(
		&id, &userID, &e.Name, &e.URL,
		&e.SigningSecretRotated, &eventTypes, &minSev, &e.Enabled,
		&lastAt, &lastCode, &lastErr,
		&e.ConsecutiveFailures, &e.AutoDisabled, &autoAt, &e.CreatedAt, &e.UpdatedAt,
		&enc,
	); err != nil {
		return domain.WebhookEndpoint{}, nil, err
	}
	e.ID = domain.WebhookEndpointID(id)
	e.UserID = domain.UserID(userID)
	e.MinSeverity = domain.NotificationSeverity(minSev)
	e.EventTypes = make([]domain.NotificationType, 0, len(eventTypes))
	for _, t := range eventTypes {
		e.EventTypes = append(e.EventTypes, domain.NotificationType(t))
	}
	e.LastDeliveryAt = lastAt
	if lastCode != nil {
		e.LastDeliveryStatusCode = int(*lastCode)
	}
	if lastErr != nil {
		e.LastDeliveryError = *lastErr
	}
	e.AutoDisabledAt = autoAt
	return e, enc, nil
}
