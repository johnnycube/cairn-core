// Package auth contains primitives shared by the authentication paths:
// password hashing, encrypted-at-rest helpers, and (later) session token
// minting. The package is deliberately small and dependency-light so it
// can be imported by both the use-case layer and any CLI subcommand that
// needs to mint a hash without spinning up the full server.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordParams describes the Argon2id parameters used to compute a hash.
// Defaults follow OWASP 2024 recommendations for low-latency interactive
// logins (m=19 MiB, t=2, p=1, keyLen=32).
//
// Values are also exposed in CAIRN_AUTH_ARGON2_* environment variables; the
// server passes its config into NewPasswordHasher at startup.
type PasswordParams struct {
	// Time is the number of passes Argon2id runs (`t`).
	Time uint32
	// Memory is the working-set size in KiB (`m`).
	Memory uint32
	// Threads is the parallelism (`p`).
	Threads uint8
	// KeyLen is the produced hash length in bytes.
	KeyLen uint32
	// SaltLen is the random salt length in bytes. 16 is standard.
	SaltLen uint32
}

// DefaultPasswordParams returns the recommended defaults for new instances.
func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		Time:    2,
		Memory:  19 * 1024, // 19 MiB
		Threads: 1,
		KeyLen:  32,
		SaltLen: 16,
	}
}

// PasswordHasher hashes and verifies passwords with a fixed parameter set.
// Verify works against hashes encoded with any (older or newer) parameter
// set, so rotating parameters does not invalidate existing users — just
// re-hash on next successful login.
type PasswordHasher struct {
	params PasswordParams
}

// NewPasswordHasher wires a hasher with the given parameters. When params
// is the zero value, DefaultPasswordParams is used.
func NewPasswordHasher(params PasswordParams) *PasswordHasher {
	if params == (PasswordParams{}) {
		params = DefaultPasswordParams()
	}
	return &PasswordHasher{params: params}
}

// Params returns the active parameters. Used by upgrade-on-login logic
// that re-hashes when a stored hash's parameters fall behind.
func (h *PasswordHasher) Params() PasswordParams { return h.params }

// Hash produces an encoded Argon2id hash string in the standard format:
//
//	$argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<salt-b64>$<key-b64>
//
// Salt is drawn from crypto/rand. Returns an error only if the entropy
// source fails.
func (h *PasswordHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("hash: password is empty")
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash: read salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		h.params.Time,
		h.params.Memory,
		h.params.Threads,
		h.params.KeyLen,
	)
	return encodeArgon2id(h.params, salt, key), nil
}

// Verify checks a candidate password against an encoded Argon2id hash.
//
// Returns (true, nil)  on match.
// Returns (false, nil) on a well-formed hash that doesn't match — the
//
//	caller should treat this as "wrong password" and
//	NOT distinguish from "user not found" in user-facing
//	messages.
//
// Returns (false, err) only when the encoded hash is malformed.
//
// The empty-string `encoded` is treated as "no password set" and returns
// (false, nil) deterministically. This lets the auth flow always call
// Verify in constant time even for users without passwords.
func (h *PasswordHasher) Verify(password, encoded string) (bool, error) {
	if encoded == "" {
		// Run a dummy compare against a sentinel hash to keep timing
		// roughly constant against a wrong-password case. We pay one
		// Argon2id round but discard the result.
		_ = argon2.IDKey(
			[]byte(password),
			make([]byte, h.params.SaltLen),
			h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen,
		)
		return false, nil
	}

	params, salt, want, err := decodeArgon2id(encoded)
	if err != nil {
		return false, fmt.Errorf("verify: %w", err)
	}

	got := argon2.IDKey(
		[]byte(password),
		salt,
		params.Time,
		params.Memory,
		params.Threads,
		uint32(len(want)),
	)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether the encoded hash was computed with
// parameters weaker than the hasher's current settings. The caller (the
// login use case) should re-hash on successful verification when this is
// true, so the on-disk hash gradually upgrades.
func (h *PasswordHasher) NeedsRehash(encoded string) bool {
	if encoded == "" {
		return false
	}
	stored, _, _, err := decodeArgon2id(encoded)
	if err != nil {
		// A malformed hash is treated as "yes, rehash" so a successful
		// login (which would itself have failed for a malformed hash)
		// triggers an upgrade path.
		return true
	}
	return stored.Time < h.params.Time ||
		stored.Memory < h.params.Memory ||
		stored.Threads < h.params.Threads ||
		stored.KeyLen < h.params.KeyLen
}

// ---------------------------------------------------------------------------
// Encoded-hash format
//
//   $argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<salt-b64>$<key-b64>
//
// The base64 here is standard-padded (RFC 4648, with =). passlib-style
// implementations omit padding; we keep it to match Go-ecosystem libraries
// like alexedwards/argon2id.
// ---------------------------------------------------------------------------

const argon2idPrefix = "$argon2id$v=19$"

func encodeArgon2id(p PasswordParams, salt, key []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func decodeArgon2id(encoded string) (PasswordParams, []byte, []byte, error) {
	if !strings.HasPrefix(encoded, argon2idPrefix) {
		return PasswordParams{}, nil, nil, errors.New("not an argon2id hash")
	}
	rest := strings.TrimPrefix(encoded, argon2idPrefix)
	parts := strings.Split(rest, "$")
	if len(parts) != 3 {
		return PasswordParams{}, nil, nil, errors.New("malformed argon2id hash: bad part count")
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[0], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("malformed argon2id hash: parse params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("malformed argon2id hash: salt b64: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("malformed argon2id hash: key b64: %w", err)
	}

	return PasswordParams{
		Time:    time,
		Memory:  memory,
		Threads: threads,
		KeyLen:  uint32(len(key)),
		SaltLen: uint32(len(salt)),
	}, salt, key, nil
}
