package httpsig

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestVerify_RequiresDigestAndDateCovered locks the rule that a signed POST must
// bind both the body (digest) and a timestamp (date). A signature that is valid
// over a reduced header set but omits either must be rejected — otherwise a
// captured request could be replayed with a swapped body.
func TestVerify_RequiresDigestAndDateCovered(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{"type":"Create"}`)

	// Sign a request over exactly the given covered-header set (forging the
	// attacker's freedom to choose a minimal set).
	signedOver := func(headers []string) *http.Request {
		req, _ := http.NewRequest("POST", "https://b.test/users/alice/inbox", bytes.NewReader(body))
		req.Header.Set("Digest", bodyDigest(body))
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		s, err := buildSigningString(req, headers)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(s))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
		req.Header.Set("Signature", fmt.Sprintf(`keyId="k",algorithm="rsa-sha256",headers=%q,signature=%q`,
			strings.Join(headers, " "), base64.StdEncoding.EncodeToString(sig)))
		return req
	}

	if err := Verify(signedOver([]string{"(request-target)", "host", "date"}), body, &priv.PublicKey); err == nil {
		t.Error("must reject a signature that does not cover digest")
	}
	if err := Verify(signedOver([]string{"(request-target)", "host", "digest"}), body, &priv.PublicKey); err == nil {
		t.Error("must reject a signature that does not cover date")
	}
	if err := Verify(signedOver([]string{"(request-target)", "host", "date", "digest"}), body, &priv.PublicKey); err != nil {
		t.Errorf("must accept when digest+date are both covered: %v", err)
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{"type":"Follow","actor":"https://a.test/users/bob"}`)

	req, _ := http.NewRequest("POST", "https://b.test/users/alice/inbox", bytes.NewReader(body))
	if err := Sign(req, "https://a.test/users/bob#main-key", priv, body); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// keyId is extractable for the verifier to fetch the actor.
	if kid, ok := KeyIDFromRequest(req); !ok || kid != "https://a.test/users/bob#main-key" {
		t.Fatalf("keyId = %q ok=%v", kid, ok)
	}

	// A correctly-signed request verifies against the matching public key.
	if err := Verify(req, body, &priv.PublicKey); err != nil {
		t.Fatalf("verify (valid): %v", err)
	}

	// A different key fails.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if err := Verify(req, body, &other.PublicKey); err == nil {
		t.Error("verify must fail against a different key")
	}

	// A tampered body fails (digest mismatch).
	if err := Verify(req, append(body, '!'), &priv.PublicKey); err == nil {
		t.Error("verify must fail when the body is tampered")
	}

	// A tampered signed header fails.
	req.Header.Set("Date", "Tue, 07 Jun 2050 00:00:00 GMT")
	if err := Verify(req, body, &priv.PublicKey); err == nil {
		t.Error("verify must fail when a signed header changes")
	}
}
