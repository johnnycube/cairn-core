package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if !VerifyPKCE(verifier, challenge, "S256") {
		t.Fatal("valid S256 verifier should pass")
	}
	if !VerifyPKCE(verifier, challenge, "") {
		t.Fatal("empty method should default to S256 and pass")
	}
	if VerifyPKCE("wrong", challenge, "S256") {
		t.Fatal("wrong verifier must fail")
	}
	if VerifyPKCE(verifier, challenge, "plain") {
		t.Fatal("plain method must be rejected (OAuth 2.1)")
	}
	if VerifyPKCE("", challenge, "S256") || VerifyPKCE(verifier, "", "S256") {
		t.Fatal("empty verifier/challenge must fail")
	}
}

func TestFilterRequestedScopes(t *testing.T) {
	clientAllowed := []string{ScopeActivitiesRead, ScopeProfileRead, ScopeActivitiesWrite}

	// Requested subset is intersected with allowed + catalog, catalog-ordered.
	got := FilterRequestedScopes([]string{ScopeProfileRead, ScopeActivitiesRead}, clientAllowed)
	want := []string{ScopeActivitiesRead, ScopeProfileRead}
	if SortedScopeString(got) != SortedScopeString(want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// A requested scope the client may not have is dropped.
	got = FilterRequestedScopes([]string{ScopeSocialWrite, ScopeActivitiesRead}, clientAllowed)
	if ScopesContain(JoinScopes(got), ScopeSocialWrite) {
		t.Fatalf("disallowed scope leaked: %v", got)
	}

	// Empty request defaults to the client's allowed scopes.
	got = FilterRequestedScopes(nil, clientAllowed)
	if len(got) != 3 {
		t.Fatalf("empty request should default to all client scopes, got %v", got)
	}
}

func TestScopeHelpers(t *testing.T) {
	if !IsWriteScope(ScopeActivitiesWrite) || IsWriteScope(ScopeActivitiesRead) {
		t.Fatal("IsWriteScope misclassified")
	}
	if !ScopesHaveAnyWrite("activities:read social:write") {
		t.Fatal("should detect a write scope")
	}
	if ScopesHaveAnyWrite("activities:read profile:read") {
		t.Fatal("should not detect a write scope among read-only")
	}
	if !ScopesContain("a:read b:read", "b:read") || ScopesContain("a:read", "b:read") {
		t.Fatal("ScopesContain wrong")
	}
}

func TestOAuthClientHelpers(t *testing.T) {
	pub := OAuthClient{ClientID: "x", RedirectURIs: []string{"https://app/cb"}, GrantTypes: []string{"authorization_code"}}
	if !pub.IsPublic() {
		t.Fatal("no secret => public")
	}
	if !pub.AllowsRedirectURI("https://app/cb") || pub.AllowsRedirectURI("https://evil/cb") {
		t.Fatal("redirect matching wrong")
	}
	if !pub.AllowsGrant("authorization_code") || pub.AllowsGrant("password") {
		t.Fatal("grant matching wrong")
	}

	secret := "s3cr3t"
	h := sha256.Sum256([]byte(secret))
	conf := OAuthClient{ClientID: "y", SecretHash: h[:]}
	if conf.IsPublic() {
		t.Fatal("with secret => confidential")
	}
	if !conf.VerifySecret(secret) || conf.VerifySecret("nope") {
		t.Fatal("secret verification wrong")
	}
}
