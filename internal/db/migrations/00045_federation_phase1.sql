-- Federation (ActivityPub) Phase 1: read-only actor + discovery. Adds the
-- per-user opt-in flag and a per-user signing keypair (the actor's publicKey).
-- See docs/federation-design.md. Inbox/outbox-write/delivery are later phases.

-- +goose Up
ALTER TABLE users ADD COLUMN federation_enabled boolean NOT NULL DEFAULT false;

-- One RSA keypair per federation-enabled user. The private key is encrypted at
-- rest with the instance master key (auth.SecretBox, AAD federation_key:<user>);
-- the public key is served in the actor document as publicKeyPem.
CREATE TABLE federation_keys (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    public_pem        text NOT NULL,
    private_encrypted bytea NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS federation_keys;
ALTER TABLE users DROP COLUMN IF EXISTS federation_enabled;
