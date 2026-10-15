package config

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// unsetEnv clears key for the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func setBaseEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"CAIRN_DATABASE_URL":               "postgres://u:p@localhost/cairn",
		"CAIRN_AUTH_SESSION_SECRET":        "secret",
		"CAIRN_AUTH_MASTER_ENCRYPTION_KEY": "key",
		"CAIRN_LOG_LEVEL":                  "debug",
		"CAIRN_LOG_FORMAT":                 "text",
		"CAIRN_EMAIL_ADAPTER":              "none",
		"CAIRN_DATABASE_MAX_CONNS":         "8",
		"CAIRN_DATABASE_MIN_CONNS":         "1",
		"CAIRN_HTTP_TLS_CERT_FILE":         "",
		"CAIRN_HTTP_TLS_KEY_FILE":          "",
		"CAIRN_HTTP_CORS_ALLOWED_ORIGINS":  "https://a.example,https://b.example",
		"CAIRN_GEOCODER_MIN_INTERVAL":      "250ms",
	} {
		t.Setenv(k, v)
	}
}

func TestLoadSuccess(t *testing.T) {
	setBaseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.URL != "postgres://u:p@localhost/cairn" || cfg.Database.MaxConns != 8 || cfg.Database.MinConns != 1 {
		t.Errorf("database = %+v", cfg.Database)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "text" {
		t.Errorf("log = %+v", cfg.Log)
	}
	if !slices.Equal(cfg.HTTP.CORSAllowedOrigins, []string{"https://a.example", "https://b.example"}) {
		t.Errorf("cors = %v", cfg.HTTP.CORSAllowedOrigins)
	}
	if cfg.Geocoder.MinInterval != 250*time.Millisecond {
		t.Errorf("geocoder interval = %v", cfg.Geocoder.MinInterval)
	}
	if cfg.Auth.SessionSecret != "secret" || cfg.Auth.MasterEncryptionKey != "key" {
		t.Errorf("auth = %+v", cfg.Auth)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		unset   []string
		wantMsg string
	}{
		{"missing database url", nil, []string{"CAIRN_DATABASE_URL", "URL"}, "required key"},
		{"unparseable int", map[string]string{"CAIRN_DATABASE_MAX_CONNS": "many"}, nil, "load config"},
		{"unparseable duration", map[string]string{"CAIRN_GEOCODER_MIN_INTERVAL": "soon"}, nil, "load config"},
		{"bad log level", map[string]string{"CAIRN_LOG_LEVEL": "trace"}, nil, "validate config: LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for k, v := range tt.set {
				t.Setenv(k, v)
			}
			for _, k := range tt.unset {
				unsetEnv(t, k)
			}
			cfg, err := Load()
			if err == nil || cfg != nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("Load() = %v, %v; want error containing %q", cfg, err, tt.wantMsg)
			}
		})
	}
}

func validConfig() Config {
	var c Config
	c.Log.Level = "info"
	c.Log.Format = "json"
	c.Email.Adapter = "none"
	c.Database.MaxConns = 10
	c.Database.MinConns = 2
	return c
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mut     func(*Config)
		wantMsg string
	}{
		{"valid", func(*Config) {}, ""},
		{"warn text", func(c *Config) { c.Log.Level, c.Log.Format = "warn", "text" }, ""},
		{"smtp with from", func(c *Config) { c.Email.Adapter, c.Email.FromAddress = "smtp", "a@b.c" }, ""},
		{"tls pair", func(c *Config) { c.HTTP.TLSCertFile, c.HTTP.TLSKeyFile = "c", "k" }, ""},
		{"equal conns", func(c *Config) { c.Database.MaxConns = 2 }, ""},
		{"bad level", func(c *Config) { c.Log.Level = "" }, "LOG_LEVEL"},
		{"bad format", func(c *Config) { c.Log.Format = "xml" }, "LOG_FORMAT"},
		{"bad adapter", func(c *Config) { c.Email.Adapter = "pigeon" }, "EMAIL_ADAPTER"},
		{"adapter without from", func(c *Config) { c.Email.Adapter = "resend" }, "EMAIL_FROM_ADDRESS"},
		{"cert without key", func(c *Config) { c.HTTP.TLSCertFile = "c" }, "TLS_CERT_FILE and HTTP_TLS_KEY_FILE"},
		{"key without cert", func(c *Config) { c.HTTP.TLSKeyFile = "k" }, "TLS_CERT_FILE and HTTP_TLS_KEY_FILE"},
		{"max below min", func(c *Config) { c.Database.MaxConns = 1 }, "DATABASE_MAX_CONNS (1) must be >= DATABASE_MIN_CONNS (2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mut(&c)
			err := c.Validate()
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %v, want %q", err, tt.wantMsg)
			}
		})
	}
}

func TestStorageConfigured(t *testing.T) {
	tests := []struct {
		id, secret string
		want       bool
	}{
		{"", "", false},
		{"id", "", false},
		{"", "secret", false},
		{"id", "secret", true},
	}
	for _, tt := range tests {
		s := StorageConfig{AccessKeyID: tt.id, SecretAccessKey: tt.secret}
		if got := s.Configured(); got != tt.want {
			t.Errorf("Configured(%q,%q) = %v", tt.id, tt.secret, got)
		}
	}
}

func TestPrintHelp(t *testing.T) {
	// Usage writes to stdout; only the error matters here.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()
	orig := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = orig }()
	if err := PrintHelp(); err != nil {
		t.Fatal(err)
	}
}

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadOIDCProvidersEmpty(t *testing.T) {
	ps, warns := LoadOIDCProviders(fakeEnv(map[string]string{"CAIRN_OIDC_PROVIDERS": "  "}))
	if ps != nil || warns != nil {
		t.Fatalf("got %v, %v", ps, warns)
	}
}

func TestLoadOIDCProviders(t *testing.T) {
	env := fakeEnv(map[string]string{
		"CAIRN_OIDC_PROVIDERS": " google, ,pocketid,google,broken ",

		"CAIRN_OIDC_GOOGLE_ISSUER_URL":    "https://accounts.google.com",
		"CAIRN_OIDC_GOOGLE_CLIENT_ID":     "gid",
		"CAIRN_OIDC_GOOGLE_CLIENT_SECRET": " gsecret ",

		"CAIRN_OIDC_POCKETID_ISSUER_URL":          "https://id.example",
		"CAIRN_OIDC_POCKETID_CLIENT_ID":           "pid",
		"CAIRN_OIDC_POCKETID_CLIENT_SECRET":       "psecret",
		"CAIRN_OIDC_POCKETID_NAME":                "Pocket ID",
		"CAIRN_OIDC_POCKETID_SCOPES":              "openid, groups,,",
		"CAIRN_OIDC_POCKETID_AUDIENCE":            "aud",
		"CAIRN_OIDC_POCKETID_SKIP_AUDIENCE_CHECK": "YES",
		"CAIRN_OIDC_POCKETID_USE_PKCE":            "off",
		"CAIRN_OIDC_POCKETID_AUTO_PROVISION":      "0",
		"CAIRN_OIDC_POCKETID_AUTO_PROVISION_ROLE": "Admin",

		"CAIRN_OIDC_BROKEN_ISSUER_URL": "https://broken.example",
	})
	ps, warns := LoadOIDCProviders(env)

	if len(ps) != 2 {
		t.Fatalf("providers = %+v", ps)
	}
	g := ps[0]
	if g.ID != "google" || g.Name != "google" || g.IssuerURL != "https://accounts.google.com" ||
		g.ClientID != "gid" || g.ClientSecret != "gsecret" || g.Audience != "" ||
		g.SkipAudienceCheck || !g.UsePKCE || !g.AutoProvision || g.AutoProvisionRole != domain.UserRoleUser {
		t.Errorf("google defaults = %+v", g)
	}
	if !slices.Equal(g.Scopes, []string{"openid", "email", "profile"}) {
		t.Errorf("google scopes = %v", g.Scopes)
	}

	p := ps[1]
	if p.ID != "pocketid" || p.Name != "Pocket ID" || p.Audience != "aud" ||
		!p.SkipAudienceCheck || p.UsePKCE || p.AutoProvision || p.AutoProvisionRole != domain.UserRoleAdmin {
		t.Errorf("pocketid = %+v", p)
	}
	if !slices.Equal(p.Scopes, []string{"openid", "groups"}) {
		t.Errorf("pocketid scopes = %v", p.Scopes)
	}

	if len(warns) != 2 ||
		!strings.Contains(warns[0], `"google" listed twice`) ||
		!strings.Contains(warns[1], `"broken" skipped: CAIRN_OIDC_BROKEN_ISSUER_URL`) {
		t.Fatalf("warnings = %q", warns)
	}
}

func TestParseBoolDefault(t *testing.T) {
	tests := []struct {
		in   string
		def  bool
		want bool
	}{
		{"true", false, true},
		{" ON ", false, true},
		{"1", false, true},
		{"no", true, false},
		{"FALSE", true, false},
		{"", true, true},
		{"", false, false},
		{"maybe", true, true},
	}
	for _, tt := range tests {
		if got := parseBoolDefault(tt.in, tt.def); got != tt.want {
			t.Errorf("parseBoolDefault(%q, %v) = %v", tt.in, tt.def, got)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV(""); got != nil {
		t.Errorf("splitCSV(\"\") = %v", got)
	}
	if got := splitCSV(" a , ,b,"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("splitCSV = %v", got)
	}
	if got := splitCSV(" , "); got != nil {
		t.Errorf("splitCSV(blank items) = %v", got)
	}
}
