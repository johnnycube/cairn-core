package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// providerMeta is a provider's OAuth2 endpoint knowledge (URLs + scopes). The
// CREDENTIALS are per-CONNECTION: a user can create several connections for the
// same provider, EACH with its own client_id/client_secret.
type providerMeta struct {
	Provider     string
	DisplayName  string
	AuthorizeURL string
	TokenURL     string
	Scopes       string
	// ActivityURLTemplate builds a deep link to the original activity on the
	// provider's site; "{id}" is replaced with the provider external id. Empty
	// when the provider has no public activity URL.
	ActivityURLTemplate string
}

var providerRegistry = map[string]providerMeta{
	"strava": {
		Provider:            "strava",
		DisplayName:         "Strava",
		AuthorizeURL:        "https://www.strava.com/oauth/authorize",
		TokenURL:            "https://www.strava.com/oauth/token",
		Scopes:              "read,activity:read_all",
		ActivityURLTemplate: "https://www.strava.com/activities/{id}",
	},
	// Credential-based provider: no OAuth endpoints. client_id/client_secret
	// hold the Garmin Connect email/password; the account is created together
	// with the connection (no authorize round-trip to link it).
	"garmin": {
		Provider:            "garmin",
		DisplayName:         "Garmin",
		ActivityURLTemplate: "https://connect.garmin.com/modern/activity/{id}",
	},
}

// oauthProvider reports whether a provider links accounts via an OAuth
// authorize round-trip (vs. credential-based providers like Garmin).
func oauthProvider(meta providerMeta) bool { return meta.AuthorizeURL != "" }

// providerActivityURL builds the external deep link for a provider's activity,
// or "" when the provider has no template.
func providerActivityURL(provider, externalID string) string {
	meta, ok := providerRegistry[provider]
	if !ok || meta.ActivityURLTemplate == "" || externalID == "" {
		return ""
	}
	return strings.ReplaceAll(meta.ActivityURLTemplate, "{id}", externalID)
}

const (
	oauthStateCookie      = "cairn_oauth_state"
	oauthConnectionCookie = "cairn_oauth_connection"
)

// mountOAuthConnect wires connection CRUD + the OAuth account-connect flow:
//
//	GET    /api/connections                       list connections + provider types
//	POST   /api/connections                       { provider, label, client_id, client_secret }
//	PUT    /api/connections/{id}                  { label, client_id, client_secret }
//	DELETE /api/connections/{id}
//	GET    /auth/oauth/{provider}/connect?connection=<id>   start OAuth for that connection
//	GET    /auth/oauth/{provider}/callback                  exchange → account (linked to connection)
func mountOAuthConnect(mux *http.ServeMux, app *App, logger *slog.Logger, publicURL string) {
	// GET /api/connections
	mux.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		conns, _ := app.UserProviderConfigs.ListForUser(r.Context(), userID)
		out := make([]map[string]any, 0, len(conns))
		for _, c := range conns {
			out = append(out, map[string]any{
				"id":           c.ID.String(),
				"provider":     c.Provider,
				"display_name": providerDisplayName(c.Provider),
				"label":        c.Label,
				"client_id":    c.ClientID,
				"has_secret":   c.HasSecret,
				"oauth":        oauthProvider(providerRegistry[c.Provider]),
			})
		}
		providers := make([]map[string]any, 0, len(providerRegistry))
		for id, meta := range providerRegistry {
			providers = append(providers, map[string]any{
				"provider": id, "display_name": meta.DisplayName, "oauth": oauthProvider(meta),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"connections": out, "providers": providers})
	})

	// GET /api/webhook-endpoints — the webhook callback URLs to register with a
	// provider's app for real-time imports. One per webhook-advertising worker
	// ({name}-{provider}); the worker owns verification/decoding. The callback
	// URL is not a secret (it's a public endpoint), so any signed-in user may
	// read it — they need it to configure their own provider app.
	mux.HandleFunc("GET /api/webhook-endpoints", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveSessionUser(r, app); !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		type ep struct {
			Provider  string `json:"provider"`
			WorkerKey string `json:"worker_key"`
			URL       string `json:"url"`
		}
		out := []ep{}
		seen := map[string]bool{}
		if app.NATSBus != nil {
			if kv, err := app.NATSBus.KV("cairn_worker_presence"); err == nil {
				keys, _ := kv.Keys(r.Context())
				for _, k := range keys {
					entry, err := kv.Get(r.Context(), k)
					if err != nil {
						continue
					}
					var hb struct {
						WorkerKey string `json:"worker_key"`
						Provider  string `json:"provider"`
						Webhooks  bool   `json:"webhooks"`
					}
					if json.Unmarshal(entry.Value, &hb) != nil || !hb.Webhooks || hb.WorkerKey == "" {
						continue
					}
					if seen[hb.WorkerKey] {
						continue
					}
					seen[hb.WorkerKey] = true
					out = append(out, ep{
						Provider:  hb.Provider,
						WorkerKey: hb.WorkerKey,
						URL:       strings.TrimRight(publicURL, "/") + "/webhooks/" + hb.WorkerKey,
					})
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"endpoints": out})
	})

	// POST /api/connections — create a connection.
	mux.HandleFunc("POST /api/connections", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Provider     string `json:"provider"`
			Label        string `json:"label"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		meta, ok := providerRegistry[body.Provider]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.ClientID) == "" {
			http.Error(w, "client_id is required", http.StatusBadRequest)
			return
		}
		if !oauthProvider(meta) && strings.TrimSpace(body.ClientSecret) == "" {
			// Credential providers: client_id/client_secret are the login —
			// without the secret the connection could never authenticate.
			http.Error(w, "client_secret is required", http.StatusBadRequest)
			return
		}
		id, err := app.UserProviderConfigs.Create(r.Context(), domain.UserProviderConfig{
			UserID: userID, Provider: body.Provider, Label: strings.TrimSpace(body.Label),
			ClientID: strings.TrimSpace(body.ClientID), ClientSecret: strings.TrimSpace(body.ClientSecret),
		})
		if err != nil {
			logger.Error("create connection failed", "error", err)
			http.Error(w, "could not create", http.StatusInternalServerError)
			return
		}
		// Credential providers have no authorize round-trip: the credentials ARE
		// the account link, so create the external account with the connection.
		if !oauthProvider(meta) && app.ExternalAccounts != nil {
			acctID, err := app.ExternalAccounts.CreateAccount(r.Context(), domain.ExternalAccount{
				UserID:            userID,
				Provider:          body.Provider,
				ProviderAccountID: strings.TrimSpace(body.ClientID),
				ConnectionID:      &id,
				DisplayLabel:      strings.TrimSpace(body.ClientID),
				Status:            domain.ExternalAccountStatusActive,
				AutoImportEnabled: true,
			})
			if err != nil {
				// Roll the config back so the user can retry cleanly instead of
				// being left with a credential-less half-connection.
				_ = app.UserProviderConfigs.Delete(r.Context(), id)
				logger.Error("create connection: account link failed", "provider", body.Provider, "error", err)
				http.Error(w, "could not create", http.StatusInternalServerError)
				return
			}
			logger.Info("connection + account created", "provider", body.Provider, "account", acctID)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id.String()})
	})

	// PUT /api/connections/{id} — update a connection (ownership-checked).
	mux.HandleFunc("PUT /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		conn, ok := ownConnection(r, app)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Label        string `json:"label"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		conn.Label = strings.TrimSpace(body.Label)
		if strings.TrimSpace(body.ClientID) != "" {
			conn.ClientID = strings.TrimSpace(body.ClientID)
		}
		conn.ClientSecret = strings.TrimSpace(body.ClientSecret) // empty keeps existing
		if err := app.UserProviderConfigs.Update(r.Context(), conn); err != nil {
			http.Error(w, "could not update", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// DELETE /api/connections/{id}
	mux.HandleFunc("DELETE /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		conn, ok := ownConnection(r, app)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := app.UserProviderConfigs.Delete(r.Context(), conn.ID); err != nil {
			http.Error(w, "could not delete", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /auth/oauth/{provider}/connect", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		meta, ok := providerRegistry[r.PathValue("provider")]
		if !ok {
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		}
		if !oauthProvider(meta) {
			http.Error(w, "provider does not use OAuth", http.StatusBadRequest)
			return
		}
		connID, err := uuid.Parse(r.URL.Query().Get("connection"))
		if err != nil {
			http.Redirect(w, r, "/connections?error=no_connection", http.StatusFound)
			return
		}
		conn, err := app.UserProviderConfigs.GetByID(r.Context(), domain.ConnectionID(connID))
		if err != nil || conn.UserID != userID || conn.Provider != meta.Provider || conn.ClientID == "" {
			http.Redirect(w, r, "/connections?error=configure_first", http.StatusFound)
			return
		}
		// Secret stored but unreadable (AAD/key change) — the token exchange in
		// the callback would fail. Tell the user to re-enter it rather than
		// bouncing them through Strava only to fail at the end.
		if conn.SecretUnreadable {
			http.Redirect(w, r, "/connections?error=secret_unreadable", http.StatusFound)
			return
		}

		state := randToken()
		http.SetCookie(w, &http.Cookie{
			Name: oauthStateCookie, Value: state, Path: "/auth/oauth/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r), MaxAge: 600,
		})
		http.SetCookie(w, &http.Cookie{
			Name: oauthConnectionCookie, Value: conn.ID.String(), Path: "/auth/oauth/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r), MaxAge: 600,
		})
		q := url.Values{}
		q.Set("client_id", conn.ClientID)
		q.Set("redirect_uri", strings.TrimRight(publicURL, "/")+"/auth/oauth/"+meta.Provider+"/callback")
		q.Set("response_type", "code")
		q.Set("scope", meta.Scopes)
		// force so the provider always shows its auth screen — lets a user pick a
		// different account for a separate connection.
		q.Set("approval_prompt", "force")
		q.Set("state", state)
		http.Redirect(w, r, meta.AuthorizeURL+"?"+q.Encode(), http.StatusFound)
	})

	mux.HandleFunc("GET /auth/oauth/{provider}/callback", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		meta, ok := providerRegistry[r.PathValue("provider")]
		if !ok {
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		}
		if !oauthProvider(meta) {
			http.Error(w, "provider does not use OAuth", http.StatusBadRequest)
			return
		}
		c, err := r.Cookie(oauthStateCookie)
		if err != nil || c.Value == "" || c.Value != r.URL.Query().Get("state") {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		cc, err := r.Cookie(oauthConnectionCookie)
		if err != nil || cc.Value == "" {
			http.Redirect(w, r, "/connections?error=no_connection", http.StatusFound)
			return
		}
		connUUID, err := uuid.Parse(cc.Value)
		if err != nil {
			http.Redirect(w, r, "/connections?error=no_connection", http.StatusFound)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			http.Redirect(w, r, "/connections?error="+url.QueryEscape(e), http.StatusFound)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		conn, err := app.UserProviderConfigs.GetByID(r.Context(), domain.ConnectionID(connUUID))
		if err != nil || conn.UserID != userID {
			http.Redirect(w, r, "/connections?error=configure_first", http.StatusFound)
			return
		}

		tok, providerAcctID, err := exchangeOAuthCode(r.Context(), meta, conn, code)
		if err != nil {
			logger.Error("oauth code exchange failed", "provider", meta.Provider, "error", err)
			http.Redirect(w, r, "/connections?error=exchange_failed", http.StatusFound)
			return
		}

		connID := conn.ID
		label := meta.DisplayName
		if conn.Label != "" {
			label = conn.Label
		}
		acctID, err := app.ExternalAccounts.CreateAccount(r.Context(), domain.ExternalAccount{
			UserID: userID, Provider: meta.Provider, ProviderAccountID: providerAcctID,
			ConnectionID: &connID, DisplayLabel: label, GrantedScopes: splitScopes(meta.Scopes),
		})
		if err != nil {
			logger.Error("create external account failed", "error", err)
			http.Error(w, "could not save account", http.StatusInternalServerError)
			return
		}
		if err := app.OAuthTokens.StoreToken(r.Context(), acctID, port.StoreTokenInput{State: tok}); err != nil {
			logger.Error("store oauth token failed", "error", err)
			http.Error(w, "could not save token", http.StatusInternalServerError)
			return
		}
		logger.Info("oauth account connected", "provider", meta.Provider, "account_id", acctID, "connection_id", connID)
		http.Redirect(w, r, "/connections?connected="+meta.Provider, http.StatusFound)
	})

	logger.Info("oauth connect endpoints mounted", "providers", len(providerRegistry))
}

// ownConnection resolves {id} and confirms it belongs to the session user.
func ownConnection(r *http.Request, app *App) (domain.UserProviderConfig, bool) {
	userID, ok := resolveSessionUser(r, app)
	if !ok {
		return domain.UserProviderConfig{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return domain.UserProviderConfig{}, false
	}
	conn, err := app.UserProviderConfigs.GetByID(r.Context(), domain.ConnectionID(id))
	if err != nil || conn.UserID != userID {
		return domain.UserProviderConfig{}, false
	}
	return conn, true
}

func providerDisplayName(provider string) string {
	if m, ok := providerRegistry[provider]; ok {
		return m.DisplayName
	}
	return provider
}

// exchangeOAuthCode posts the authorization code to the provider's token
// endpoint using the connection's app credentials.
func exchangeOAuthCode(ctx context.Context, meta providerMeta, conn domain.UserProviderConfig, code string) (port.TokenState, string, error) {
	form := url.Values{}
	form.Set("client_id", conn.ClientID)
	form.Set("client_secret", conn.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return port.TokenState{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return port.TokenState{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return port.TokenState{}, "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		Athlete      struct {
			ID int64 `json:"id"`
		} `json:"athlete"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return port.TokenState{}, "", err
	}

	exp := time.Unix(body.ExpiresAt, 0)
	if body.ExpiresAt == 0 && body.ExpiresIn > 0 {
		exp = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	ts := port.TokenState{
		AccessToken: body.AccessToken, RefreshToken: body.RefreshToken,
		ExpiresAt: exp, Scope: body.Scope, TokenType: body.TokenType,
	}
	acctID := ""
	if body.Athlete.ID != 0 {
		acctID = strconv.FormatInt(body.Athlete.ID, 10)
	}
	return ts, acctID, nil
}

func splitScopes(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
