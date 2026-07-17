package config

import (
	"fmt"
	"strings"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// LoadOIDCProviders parses the env-declared OIDC providers. Config is env-only:
//
//	CAIRN_OIDC_PROVIDERS              comma-separated ids (e.g. "pocketid,google,apple")
//	CAIRN_OIDC_<ID>_NAME             login-button label (default: id)
//	CAIRN_OIDC_<ID>_ISSUER_URL       OIDC issuer (required)
//	CAIRN_OIDC_<ID>_CLIENT_ID        OAuth client id (required)
//	CAIRN_OIDC_<ID>_CLIENT_SECRET    confidential client secret (required)
//	CAIRN_OIDC_<ID>_SCOPES           comma-separated (default: openid,email,profile)
//	CAIRN_OIDC_<ID>_AUDIENCE         expected aud (default: client id)
//	CAIRN_OIDC_<ID>_SKIP_AUDIENCE_CHECK  bool (default false)
//	CAIRN_OIDC_<ID>_USE_PKCE         bool (default true)
//	CAIRN_OIDC_<ID>_AUTO_PROVISION   bool (default true)
//	CAIRN_OIDC_<ID>_AUTO_PROVISION_ROLE  user|admin (default user)
//
// <ID> in the per-provider keys is the upper-cased provider id (google →
// CAIRN_OIDC_GOOGLE_ISSUER_URL). getenv is injected for testability (pass
// os.Getenv in production). Returns the valid providers plus a slice of
// human-readable warnings for malformed/incomplete entries (so startup can log
// them without failing the whole instance).
func LoadOIDCProviders(getenv func(string) string) ([]domain.OIDCProvider, []string) {
	raw := strings.TrimSpace(getenv("CAIRN_OIDC_PROVIDERS"))
	if raw == "" {
		return nil, nil
	}

	var providers []domain.OIDCProvider
	var warnings []string
	seen := map[string]bool{}

	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if seen[id] {
			warnings = append(warnings, fmt.Sprintf("oidc provider %q listed twice; ignoring the duplicate", id))
			continue
		}
		seen[id] = true

		key := func(suffix string) string {
			return "CAIRN_OIDC_" + strings.ToUpper(id) + "_" + suffix
		}
		get := func(suffix string) string { return strings.TrimSpace(getenv(key(suffix))) }

		issuer := get("ISSUER_URL")
		clientID := get("CLIENT_ID")
		clientSecret := get("CLIENT_SECRET")
		if issuer == "" || clientID == "" || clientSecret == "" {
			warnings = append(warnings, fmt.Sprintf(
				"oidc provider %q skipped: %s_ISSUER_URL, _CLIENT_ID and _CLIENT_SECRET are all required",
				id, "CAIRN_OIDC_"+strings.ToUpper(id)))
			continue
		}

		name := get("NAME")
		if name == "" {
			name = id
		}
		scopes := splitCSV(get("SCOPES"))
		if len(scopes) == 0 {
			scopes = []string{"openid", "email", "profile"}
		}
		role := domain.UserRole(strings.ToLower(get("AUTO_PROVISION_ROLE")))
		if role != domain.UserRoleAdmin {
			role = domain.UserRoleUser
		}

		providers = append(providers, domain.OIDCProvider{
			ID:                id,
			Name:              name,
			IssuerURL:         issuer,
			ClientID:          clientID,
			ClientSecret:      clientSecret,
			Scopes:            scopes,
			Audience:          get("AUDIENCE"),
			SkipAudienceCheck: parseBoolDefault(get("SKIP_AUDIENCE_CHECK"), false),
			UsePKCE:           parseBoolDefault(get("USE_PKCE"), true),
			AutoProvision:     parseBoolDefault(get("AUTO_PROVISION"), true),
			AutoProvisionRole: role,
		})
	}
	return providers, warnings
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBoolDefault(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
	}
}
