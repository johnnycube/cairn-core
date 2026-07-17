package auth

import (
	"strings"
	"testing"
)

func TestPasswordHasher_RoundTrip(t *testing.T) {
	h := NewPasswordHasher(PasswordParams{})

	encoded, err := h.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("encoded hash has wrong prefix: %q", encoded)
	}

	ok, err := h.Verify("hunter2", encoded)
	if err != nil {
		t.Fatalf("Verify good password: %v", err)
	}
	if !ok {
		t.Fatalf("Verify good password: returned false")
	}

	ok, err = h.Verify("wrong", encoded)
	if err != nil {
		t.Fatalf("Verify bad password: %v", err)
	}
	if ok {
		t.Fatalf("Verify bad password: returned true")
	}
}

func TestPasswordHasher_EmptyEncodedReturnsFalseNoError(t *testing.T) {
	h := NewPasswordHasher(PasswordParams{})
	ok, err := h.Verify("anything", "")
	if err != nil {
		t.Fatalf("Verify against empty: %v", err)
	}
	if ok {
		t.Fatalf("Verify against empty: returned true")
	}
}

func TestPasswordHasher_MalformedHash(t *testing.T) {
	h := NewPasswordHasher(PasswordParams{})
	if _, err := h.Verify("p", "not-a-hash"); err == nil {
		t.Fatalf("Verify against malformed hash: want error, got nil")
	}
}

func TestPasswordHasher_NeedsRehash(t *testing.T) {
	weak := NewPasswordHasher(PasswordParams{
		Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16,
	})
	strong := NewPasswordHasher(PasswordParams{
		Time: 4, Memory: 64 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16,
	})

	encoded, err := weak.Hash("password")
	if err != nil {
		t.Fatalf("Hash with weak params: %v", err)
	}

	if !strong.NeedsRehash(encoded) {
		t.Fatalf("strong hasher should report needs-rehash for weak hash")
	}
	if weak.NeedsRehash(encoded) {
		t.Fatalf("weak hasher should not report needs-rehash for its own hash")
	}
}
