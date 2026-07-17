package postgres

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// FederationKeyRepo persists per-user ActivityPub signing keypairs. The private
// key is AES-GCM encrypted at rest (auth.SecretBox, AAD bound to the user id);
// the public key is stored + served as a PKIX/SPKI PEM (the publicKeyPem an AP
// actor advertises). Keys are generated lazily on first request.
type FederationKeyRepo struct {
	pool    *pgxpool.Pool
	secrets *auth.SecretBox
}

func NewFederationKeyRepo(pool *pgxpool.Pool, secrets *auth.SecretBox) *FederationKeyRepo {
	return &FederationKeyRepo{pool: pool, secrets: secrets}
}

func keyAAD(userID domain.UserID) []byte { return []byte("federation_key:" + userID.String()) }

func (r *FederationKeyRepo) GetOrCreatePublicPEM(ctx context.Context, userID domain.UserID) (string, error) {
	db := dbtx(ctx, r.pool)

	var publicPEM string
	err := db.QueryRow(ctx,
		`SELECT public_pem FROM federation_keys WHERE user_id = $1`, userID.UUID(),
	).Scan(&publicPEM)
	if err == nil {
		return publicPEM, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("load federation key: %w", err)
	}
	if r.secrets == nil {
		return "", errors.New("federation key generation requires a master encryption key")
	}

	// First use → generate, encrypt the private half, persist.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("generate federation key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privPKCS8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	ct, err := r.secrets.Encrypt(privPKCS8, keyAAD(userID))
	if err != nil {
		return "", fmt.Errorf("encrypt private key: %w", err)
	}

	// ON CONFLICT DO NOTHING + re-read handles a concurrent first-use race:
	// whoever lost the insert reads the winner's public key.
	if _, err := db.Exec(ctx,
		`INSERT INTO federation_keys (user_id, public_pem, private_encrypted)
		 VALUES ($1, $2, $3) ON CONFLICT (user_id) DO NOTHING`,
		userID.UUID(), publicPEM, ct,
	); err != nil {
		return "", fmt.Errorf("store federation key: %w", err)
	}
	if err := db.QueryRow(ctx,
		`SELECT public_pem FROM federation_keys WHERE user_id = $1`, userID.UUID(),
	).Scan(&publicPEM); err != nil {
		return "", fmt.Errorf("reload federation key: %w", err)
	}
	return publicPEM, nil
}

// GetPrivateKey decrypts and parses the user's RSA private key for signing.
func (r *FederationKeyRepo) GetPrivateKey(ctx context.Context, userID domain.UserID) (*rsa.PrivateKey, error) {
	db := dbtx(ctx, r.pool)
	var ct []byte
	err := db.QueryRow(ctx,
		`SELECT private_encrypted FROM federation_keys WHERE user_id = $1`, userID.UUID(),
	).Scan(&ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load federation private key: %w", err)
	}
	if r.secrets == nil {
		return nil, errors.New("no master encryption key configured")
	}
	pkcs8, err := r.secrets.Decrypt(ct, keyAAD(userID))
	if err != nil {
		return nil, fmt.Errorf("decrypt federation private key: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("parse federation private key: %w", err)
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("federation key is not RSA")
	}
	return rk, nil
}
