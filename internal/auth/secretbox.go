// Package auth holds primitives shared across the secondary adapters
// for authentication, encryption, and password hashing.
//
// The Argon2id password hasher lives in password.go. This file
// (secretbox.go) provides AES-256-GCM symmetric encryption used to
// protect secrets at rest in the DB: OAuth tokens for external accounts,
// the NATS account signing-key seed, and (in the future) any per-user
// secrets like backup encryption keys.
//
// Threat model:
//
//   - DB compromise without app compromise → attacker sees only ciphertext.
//   - App compromise → game over (key is in process memory at use time).
//   - Operator who can read the host environment can decrypt; that's
//     intentional, the operator runs the service.
//
// Wire format of every ciphertext:
//
//	[nonce | ciphertext+tag]
//	   12 bytes        N bytes
//
// AES-GCM produces a 16-byte tag appended to the ciphertext; the standard
// library combines them in Seal(). On Open() we split nonce off the front.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// SecretBox is a value-object wrapping the derived 32-byte AES key.
// Construct once at startup with NewSecretBoxFromMasterKey; share the
// pointer across adapters that need to encrypt/decrypt.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBoxFromMasterKey derives a 32-byte AES key from the given
// material and constructs a SecretBox.
//
// The material may be:
//   - A base64-encoded 32-byte key (preferred; produced by
//     `head -c 32 /dev/urandom | base64`)
//   - Any other string, in which case SHA-256(material) is used as the
//     key. Use the base64 path in production; the SHA-256 fallback is
//     a convenience for development environments where an operator
//     hasn't generated a proper key yet.
//
// Returns an error if the material is empty.
func NewSecretBoxFromMasterKey(material string) (*SecretBox, error) {
	if material == "" {
		return nil, errors.New("secretbox: master key material is empty")
	}

	var key [32]byte
	if decoded, err := base64.StdEncoding.DecodeString(material); err == nil && len(decoded) == 32 {
		copy(key[:], decoded)
	} else {
		// Fallback: hash the passphrase to derive a 32-byte key. Stable
		// across restarts as long as the passphrase doesn't change.
		sum := sha256.Sum256([]byte(material))
		key = sum
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("secretbox: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: cipher.NewGCM: %w", err)
	}
	return &SecretBox{aead: gcm}, nil
}

// Encrypt seals plaintext + associated-data into a fresh ciphertext.
// The returned []byte starts with the 12-byte nonce so Decrypt can
// recover it. The associated-data is authenticated but not encrypted;
// callers typically pass nil or a context tag (e.g. "oauth_token:strava")
// to bind the ciphertext to its semantic location.
//
// Each call generates a fresh random nonce via crypto/rand. Reusing a
// (key, nonce) pair would catastrophically break GCM security, hence
// the per-call fresh nonce.
func (b *SecretBox) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, associatedData)
	return sealed, nil
}

// Decrypt opens a ciphertext produced by Encrypt. AssociatedData must
// match what was passed to Encrypt; mismatch fails the GCM tag check
// and Decrypt returns an error.
//
// Returns an error (without leaking details about which check failed)
// for any malformation, tag mismatch, or wrong key.
func (b *SecretBox) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	nonceSize := b.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := b.aead.Open(nil, nonce, body, associatedData)
	if err != nil {
		return nil, errors.New("secretbox: open failed")
	}
	return plaintext, nil
}
