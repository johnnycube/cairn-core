// Package httpsig implements the subset of HTTP Signatures (draft-cavage) that
// ActivityPub server-to-server uses: RSA-SHA256 over (request-target) + host +
// date + digest, with a SHA-256 Digest of the body. Stdlib-only.
//
// Sign is used for outbound delivery (signing with a local user's private key);
// Verify authenticates an inbound activity against the sending actor's public
// key (fetched by the caller from the keyId).
package httpsig

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// signedHeaders is the ordered set we sign for a POST with a body.
var signedHeaders = []string{"(request-target)", "host", "date", "digest"}

// Sign adds Date, Digest and Signature headers to req (which must have a body
// of `body` and a URL with a host). keyID is the actor's key URL
// (e.g. https://host/users/alice#main-key).
func Sign(req *http.Request, keyID string, priv *rsa.PrivateKey, body []byte) error {
	if req.Header.Get("Date") == "" {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	req.Header.Set("Digest", bodyDigest(body))
	if req.Header.Get("Host") == "" {
		req.Host = req.URL.Host
	}

	signingString, err := buildSigningString(req, signedHeaders)
	if err != nil {
		return err
	}
	h := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId=%q,algorithm="rsa-sha256",headers=%q,signature=%q`,
		keyID, strings.Join(signedHeaders, " "), base64.StdEncoding.EncodeToString(sig),
	))
	return nil
}

// KeyIDFromRequest extracts the keyId from a request's Signature header so the
// caller can fetch the corresponding actor + public key before Verify.
func KeyIDFromRequest(req *http.Request) (string, bool) {
	params := parseSignatureHeader(req.Header.Get("Signature"))
	k, ok := params["keyId"]
	return k, ok && k != ""
}

// Verify authenticates req against pub using its Signature header. body is the
// raw request body (already read) — its SHA-256 must match the Digest header.
// It also bounds clock skew to 5 minutes.
func Verify(req *http.Request, body []byte, pub *rsa.PublicKey) error {
	params := parseSignatureHeader(req.Header.Get("Signature"))
	if params["keyId"] == "" || params["signature"] == "" {
		return fmt.Errorf("missing/invalid Signature header")
	}
	headers := strings.Fields(params["headers"])
	if len(headers) == 0 {
		headers = []string{"date"}
	}

	// A signed POST must bind BOTH the body (digest) and a timestamp (date)
	// into the signature. If either is left out of the covered-headers set, a
	// captured request can be replayed — with a swapped body, or indefinitely.
	// Require both to be covered, not just honoured-if-present.
	if !containsFold(headers, "digest") {
		return fmt.Errorf("signature does not cover digest")
	}
	if req.Header.Get("Digest") != bodyDigest(body) {
		return fmt.Errorf("digest mismatch")
	}
	if !containsFold(headers, "date") {
		return fmt.Errorf("signature does not cover date")
	}
	t, err := http.ParseTime(req.Header.Get("Date"))
	if err != nil {
		return fmt.Errorf("invalid Date header: %w", err)
	}
	if skew := time.Since(t); skew > 5*time.Minute || skew < -5*time.Minute {
		return fmt.Errorf("date skew too large: %s", skew)
	}

	signingString, err := buildSigningString(req, headers)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	h := sha256.Sum256([]byte(signingString))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

func bodyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

// buildSigningString assembles the draft-cavage signing string for the given
// header list. (request-target) is synthesised from the method + path; other
// names are read from the request header (host falls back to req.Host/URL).
func buildSigningString(req *http.Request, headers []string) (string, error) {
	var lines []string
	for _, name := range headers {
		ln := strings.ToLower(name)
		switch ln {
		case "(request-target)":
			path := req.URL.RequestURI()
			lines = append(lines, "(request-target): "+strings.ToLower(req.Method)+" "+path)
		case "host":
			host := req.Header.Get("Host")
			if host == "" {
				host = req.Host
			}
			if host == "" {
				host = req.URL.Host
			}
			lines = append(lines, "host: "+host)
		default:
			v := req.Header.Get(name)
			if v == "" {
				return "", fmt.Errorf("signed header %q missing from request", name)
			}
			lines = append(lines, ln+": "+v)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// parseSignatureHeader splits `keyId="x",headers="a b",signature="y"` into a map.
func parseSignatureHeader(h string) map[string]string {
	out := map[string]string{}
	for _, part := range splitTopLevel(h) {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		v = strings.Trim(v, `"`)
		out[k] = v
	}
	return out
}

// splitTopLevel splits on commas that are not inside quotes (a signature's
// base64 can contain neither, but headers values shouldn't break this anyway).
func splitTopLevel(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
			} else {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func containsFold(xs []string, want string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, want) {
			return true
		}
	}
	return false
}
