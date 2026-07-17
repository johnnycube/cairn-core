package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// UserProviderConfigRepo implements port.UserProviderConfigRepo. Each row is one
// connection; client_secret is encrypted at rest (AES-GCM via auth.SecretBox),
// AAD-bound to the connection id so ciphertext can't be relocated between rows.
type UserProviderConfigRepo struct {
	pool    *pgxpool.Pool
	secrets *auth.SecretBox
}

func NewUserProviderConfigRepo(pool *pgxpool.Pool, secrets *auth.SecretBox) *UserProviderConfigRepo {
	return &UserProviderConfigRepo{pool: pool, secrets: secrets}
}

func connAAD(id domain.ConnectionID) []byte {
	return []byte("user_provider_config:" + id.String())
}

func marshalConfig(c map[string]string) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	if len(b) == 0 || string(b) == "null" {
		b = []byte("{}")
	}
	return b, nil
}

func (r *UserProviderConfigRepo) Create(ctx context.Context, cfg domain.UserProviderConfig) (domain.ConnectionID, error) {
	id := domain.ConnectionID(uuid.New())
	configJSON, err := marshalConfig(cfg.Config)
	if err != nil {
		return domain.ConnectionID{}, err
	}
	var secretEnc []byte
	if cfg.ClientSecret != "" {
		enc, err := r.secrets.Encrypt([]byte(cfg.ClientSecret), connAAD(id))
		if err != nil {
			return domain.ConnectionID{}, fmt.Errorf("encrypt client secret: %w", err)
		}
		secretEnc = enc
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO user_provider_configs (id, user_id, provider, label, client_id, client_secret_encrypted, config)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id.UUID(), cfg.UserID.UUID(), cfg.Provider, cfg.Label, cfg.ClientID, secretEnc, configJSON)
	if err != nil {
		return domain.ConnectionID{}, fmt.Errorf("create connection: %w", err)
	}
	return id, nil
}

func (r *UserProviderConfigRepo) Update(ctx context.Context, cfg domain.UserProviderConfig) error {
	configJSON, err := marshalConfig(cfg.Config)
	if err != nil {
		return err
	}
	var secretEnc []byte
	if cfg.ClientSecret != "" {
		enc, err := r.secrets.Encrypt([]byte(cfg.ClientSecret), connAAD(cfg.ID))
		if err != nil {
			return fmt.Errorf("encrypt client secret: %w", err)
		}
		secretEnc = enc
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE user_provider_configs SET
			label = $2,
			client_id = $3,
			client_secret_encrypted = COALESCE($4, client_secret_encrypted),
			config = $5,
			updated_at = now()
		WHERE id = $1`,
		cfg.ID.UUID(), cfg.Label, cfg.ClientID, secretEnc, configJSON)
	if err != nil {
		return fmt.Errorf("update connection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserProviderConfigRepo) GetByID(ctx context.Context, id domain.ConnectionID) (domain.UserProviderConfig, error) {
	var (
		secretEnc  []byte
		configJSON []byte
		cfg        = domain.UserProviderConfig{ID: id}
		uid        uuid.UUID
	)
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, provider, label, client_id, client_secret_encrypted, config, created_at, updated_at
		FROM user_provider_configs WHERE id=$1`, id.UUID()).
		Scan(&uid, &cfg.Provider, &cfg.Label, &cfg.ClientID, &secretEnc, &configJSON, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.UserProviderConfig{}, domain.ErrNotFound
		}
		return domain.UserProviderConfig{}, fmt.Errorf("get connection: %w", err)
	}
	cfg.UserID = domain.UserID(uid)
	if len(secretEnc) > 0 {
		cfg.HasSecret = true
		plain, err := r.secrets.Decrypt(secretEnc, connAAD(id))
		if err != nil {
			// A stored-but-undecryptable secret (AAD/key change) must not brick
			// connection management — the secret is only needed at token
			// exchange. Surface it as a flag so callers can prompt a re-save.
			cfg.SecretUnreadable = true
		} else {
			cfg.ClientSecret = string(plain)
		}
	}
	_ = json.Unmarshal(configJSON, &cfg.Config)
	return cfg, nil
}

func (r *UserProviderConfigRepo) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.UserProviderConfig, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, provider, label, client_id, (client_secret_encrypted IS NOT NULL), config, created_at, updated_at
		FROM user_provider_configs WHERE user_id=$1 ORDER BY provider, label, created_at`, userID.UUID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UserProviderConfig
	for rows.Next() {
		cfg := domain.UserProviderConfig{UserID: userID}
		var id uuid.UUID
		var configJSON []byte
		if err := rows.Scan(&id, &cfg.Provider, &cfg.Label, &cfg.ClientID, &cfg.HasSecret, &configJSON, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, err
		}
		cfg.ID = domain.ConnectionID(id)
		_ = json.Unmarshal(configJSON, &cfg.Config)
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (r *UserProviderConfigRepo) Delete(ctx context.Context, id domain.ConnectionID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM user_provider_configs WHERE id=$1`, id.UUID())
	return err
}
