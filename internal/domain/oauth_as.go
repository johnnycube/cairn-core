package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OAuth 2.1 authorization-server domain
//
// Cairn is an OAuth 2.1 authorization server for native apps, third-party
// clients and MCP agents. These are pure types + scope/PKCE helpers; storage
// lives in port.OAuthServerRepo and the postgres adapter, the flow in
// cmd/server/oauth_as.go.
// ---------------------------------------------------------------------------

// Scope catalog. Access tokens carry a space-separated subset; the API and the
// MCP server enforce them. Read-only is the safe default for agents.
const (
	ScopeActivitiesRead  = "activities:read"
	ScopeActivitiesWrite = "activities:write"
	ScopeProfileRead     = "profile:read"
	ScopeSocialRead      = "social:read"
	ScopeSocialWrite     = "social:write"
	ScopeSegmentsRead    = "segments:read"
	ScopeTrainingRead    = "training:read"
)

// SupportedScopes is the full catalog advertised in AS metadata.
var SupportedScopes = []string{
	ScopeActivitiesRead,
	ScopeActivitiesWrite,
	ScopeProfileRead,
	ScopeSocialRead,
	ScopeSocialWrite,
	ScopeSegmentsRead,
	ScopeTrainingRead,
}

// ReadOnlyScopes is the default grant for agents (no write capability).
var ReadOnlyScopes = []string{
	ScopeActivitiesRead,
	ScopeProfileRead,
	ScopeSocialRead,
	ScopeSegmentsRead,
	ScopeTrainingRead,
}

// ScopeSupported reports whether s is a known scope.
func ScopeSupported(s string) bool {
	for _, x := range SupportedScopes {
		if x == s {
			return true
		}
	}
	return false
}

// IsWriteScope reports whether s grants mutation.
func IsWriteScope(s string) bool { return strings.HasSuffix(s, ":write") }

// ParseScopes splits a space-separated scope string, dropping blanks.
func ParseScopes(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		out = append(out, f)
	}
	return out
}

// JoinScopes renders scopes as the space-separated wire form.
func JoinScopes(scopes []string) string { return strings.Join(scopes, " ") }

// ScopesContain reports whether the space-separated scope string grants want.
func ScopesContain(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}

// ScopesHaveAnyWrite reports whether the space-separated scope string grants
// any mutation scope.
func ScopesHaveAnyWrite(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if IsWriteScope(s) {
			return true
		}
	}
	return false
}

// FilterRequestedScopes intersects the requested scopes with what the client is
// allowed and what Cairn supports, preserving catalog order and de-duping. An
// empty request defaults to the client's allowed scopes.
func FilterRequestedScopes(requested, clientAllowed []string) []string {
	allowed := map[string]bool{}
	for _, s := range clientAllowed {
		allowed[s] = true
	}
	want := map[string]bool{}
	if len(requested) == 0 {
		for _, s := range clientAllowed {
			want[s] = true
		}
	} else {
		for _, s := range requested {
			want[s] = true
		}
	}
	var out []string
	for _, s := range SupportedScopes {
		if want[s] && (len(clientAllowed) == 0 || allowed[s]) {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// OAuthClient is a registered OAuth client. ClientID is the public identifier.
// A public client (SecretHash == nil) must use PKCE; a confidential client
// authenticates at the token endpoint with its secret.
type OAuthClient struct {
	ClientID                string
	SecretHash              []byte // nil => public client (PKCE-only)
	Name                    string
	RedirectURIs            []string
	GrantTypes              []string
	Scopes                  []string // scopes the client may request
	TokenEndpointAuthMethod string   // none | client_secret_basic | client_secret_post
	IsDynamic               bool
	CreatedBy               *UserID
	CreatedAt               time.Time
}

// IsPublic reports whether the client has no secret (PKCE-only).
func (c OAuthClient) IsPublic() bool { return len(c.SecretHash) == 0 }

// AllowsRedirectURI reports whether uri exactly matches a registered redirect.
func (c OAuthClient) AllowsRedirectURI(uri string) bool {
	for _, u := range c.RedirectURIs {
		if u == uri {
			return true
		}
	}
	return false
}

// AllowsGrant reports whether the client may use grant type g.
func (c OAuthClient) AllowsGrant(g string) bool {
	for _, x := range c.GrantTypes {
		if x == g {
			return true
		}
	}
	return false
}

// VerifySecret constant-time compares a presented secret against the stored
// hash. Always false for public clients.
func (c OAuthClient) VerifySecret(secret string) bool {
	if c.IsPublic() {
		return false
	}
	h := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(h[:], c.SecretHash) == 1
}

// ---------------------------------------------------------------------------
// Authorization code
// ---------------------------------------------------------------------------

// OAuthAuthorizationCode is a one-time code exchanged for tokens. Only its hash
// is stored.
type OAuthAuthorizationCode struct {
	ClientID            string
	UserID              UserID
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
}

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

// OAuthAccessToken authorizes API/MCP calls as UserID, bounded by Scope.
type OAuthAccessToken struct {
	ClientID  string
	UserID    UserID
	Scope     string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// IsValidAt reports whether the token authenticates at now.
func (t OAuthAccessToken) IsValidAt(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return now.Before(t.ExpiresAt)
}

// HasScope reports whether the token grants want.
func (t OAuthAccessToken) HasScope(want string) bool { return ScopesContain(t.Scope, want) }

// OAuthRefreshToken mints fresh access tokens without re-consent.
type OAuthRefreshToken struct {
	ClientID  string
	UserID    UserID
	Scope     string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// IsValidAt reports whether the refresh token is usable at now.
func (t OAuthRefreshToken) IsValidAt(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return now.Before(t.ExpiresAt)
}

// ---------------------------------------------------------------------------
// PKCE
// ---------------------------------------------------------------------------

// VerifyPKCE checks a code_verifier against the stored code_challenge. Only the
// S256 method is accepted (OAuth 2.1 forbids "plain").
func VerifyPKCE(verifier, challenge, method string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	if method != "" && method != "S256" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// SortedScopeString normalizes a scope slice to a stable space-separated form.
func SortedScopeString(scopes []string) string {
	cp := append([]string(nil), scopes...)
	sort.Strings(cp)
	return strings.Join(cp, " ")
}
