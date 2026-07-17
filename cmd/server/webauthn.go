package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// WebAuthn / passkey endpoints (REST — go-webauthn already emits the standard
// WebAuthn JSON the browser's navigator.credentials consumes, so a typed proto
// wrapper would be pure overhead).
//
//	POST /auth/webauthn/register/start    (authed) → CredentialCreation options
//	POST /auth/webauthn/register/finish   (authed) ← attestation; stores passkey
//	POST /auth/webauthn/login/start                → CredentialAssertion options
//	POST /auth/webauthn/login/finish               ← assertion; issues a session
//	GET    /api/passkeys                  (authed) → list
//	PUT    /api/passkeys/{id}             (authed) ← {name}; rename
//	DELETE /api/passkeys/{id}             (authed)
const (
	waRegCookie   = "cairn_wa_reg"
	waLoginCookie = "cairn_wa_login"
)

func mountWebAuthn(mux *http.ServeMux, app *App, logger *slog.Logger, publicURL string, sessionTTL time.Duration) {
	if app.Passkeys == nil || app.Users == nil || app.Sessions == nil {
		return
	}
	wa, err := newWebAuthn(publicURL)
	if err != nil {
		logger.Error("webauthn disabled: bad config", "error", err)
		return
	}

	// ---- registration (authenticated) ------------------------------------
	mux.HandleFunc("POST /auth/webauthn/register/start", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		waUser, _, err := loadWAUser(r, app, userID)
		if err != nil {
			http.Error(w, "user load failed", http.StatusInternalServerError)
			return
		}
		// Exclude already-registered credentials; require a discoverable
		// (resident) key so this passkey also works for usernameless login.
		exclusions := make([]protocol.CredentialDescriptor, 0, len(waUser.creds))
		for _, c := range waUser.creds {
			exclusions = append(exclusions, c.Descriptor())
		}
		options, session, err := wa.BeginRegistration(waUser,
			webauthn.WithExclusions(exclusions),
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		)
		if err != nil {
			http.Error(w, "begin registration failed", http.StatusInternalServerError)
			return
		}
		storeWASession(w, r, waRegCookie, session)
		writeJSON(w, http.StatusOK, options)
	})

	mux.HandleFunc("POST /auth/webauthn/register/finish", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		session, err := loadWASession(r, waRegCookie)
		if err != nil {
			http.Error(w, "no registration in progress", http.StatusBadRequest)
			return
		}
		waUser, _, err := loadWAUser(r, app, userID)
		if err != nil {
			http.Error(w, "user load failed", http.StatusInternalServerError)
			return
		}
		cred, err := wa.FinishRegistration(waUser, *session, r)
		if err != nil {
			logger.Warn("webauthn registration failed", "error", err)
			http.Error(w, "registration verification failed", http.StatusBadRequest)
			return
		}
		credJSON, err := json.Marshal(cred)
		if err != nil {
			http.Error(w, "encode credential failed", http.StatusInternalServerError)
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			name = "Passkey"
		}
		if _, err := app.Passkeys.Create(r.Context(), domain.Passkey{
			UserID: userID, CredentialID: cred.ID, CredentialJSON: credJSON, Name: name,
		}); err != nil {
			http.Error(w, "store passkey failed", http.StatusInternalServerError)
			return
		}
		clearWACookie(w, r, waRegCookie)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// ---- passwordless login ----------------------------------------------
	mux.HandleFunc("POST /auth/webauthn/login/start", func(w http.ResponseWriter, r *http.Request) {
		options, session, err := wa.BeginDiscoverableLogin()
		if err != nil {
			http.Error(w, "begin login failed", http.StatusInternalServerError)
			return
		}
		storeWASession(w, r, waLoginCookie, session)
		writeJSON(w, http.StatusOK, options)
	})

	mux.HandleFunc("POST /auth/webauthn/login/finish", func(w http.ResponseWriter, r *http.Request) {
		session, err := loadWASession(r, waLoginCookie)
		if err != nil {
			http.Error(w, "no login in progress", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		// DiscoverableUserHandler resolves the user from the credential's user
		// handle (which we set to the user's UUID at registration).
		handler := func(rawID, userHandle []byte) (webauthn.User, error) {
			uid, err := uuid.FromBytes(userHandle)
			if err != nil {
				return nil, err
			}
			waUser, _, err := loadWAUser(r, app, domain.UserID(uid))
			return waUser, err
		}
		cred, err := wa.FinishDiscoverableLogin(handler, *session, r)
		if err != nil {
			logger.Warn("webauthn login failed", "error", err)
			http.Error(w, "login verification failed", http.StatusUnauthorized)
			return
		}
		// Persist the advanced sign counter, and resolve the owning user.
		credJSON, _ := json.Marshal(cred)
		_ = app.Passkeys.UpdateCredential(ctx, cred.ID, credJSON)
		pk, err := app.Passkeys.GetByCredentialID(ctx, cred.ID)
		if err != nil {
			http.Error(w, "credential not found", http.StatusUnauthorized)
			return
		}
		clearWACookie(w, r, waLoginCookie)
		if err := issueSessionForUser(w, r, app, pk.UserID, domain.SessionAuthPasskey, sessionTTL); err != nil {
			logger.Error("webauthn login: issue session failed", "error", err)
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// ---- passkey management (authenticated) ------------------------------
	mux.HandleFunc("GET /api/passkeys", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		pks, err := app.Passkeys.ListByUser(r.Context(), userID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(pks))
		for _, p := range pks {
			var lastUsed any
			if p.LastUsedAt != nil {
				lastUsed = p.LastUsedAt.UTC().Format(time.RFC3339)
			}
			out = append(out, map[string]any{
				"id": p.ID.String(), "name": p.Name,
				"created_at": p.CreatedAt.UTC().Format(time.RFC3339), "last_used_at": lastUsed,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"passkeys": out})
	})

	mux.HandleFunc("PUT /api/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := app.Passkeys.Rename(r.Context(), userID, domain.PasskeyID(id), strings.TrimSpace(body.Name)); err != nil {
			http.Error(w, "rename failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("DELETE /api/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := app.Passkeys.Delete(r.Context(), userID, domain.PasskeyID(id)); err != nil {
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("webauthn endpoints mounted", "rpid", wa.Config.RPID, "origins", wa.Config.RPOrigins)
}

// webAuthnUser adapts a Cairn user + its passkeys to the webauthn.User interface.
type webAuthnUser struct {
	id      domain.UserID
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte {
	b := u.id.UUID()
	return b[:]
}
func (u *webAuthnUser) WebAuthnName() string                       { return u.name }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.display }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }
func (u *webAuthnUser) WebAuthnIcon() string                       { return "" }

// loadWAUser hydrates a webAuthnUser (user + decoded passkey credentials).
func loadWAUser(r *http.Request, app *App, userID domain.UserID) (*webAuthnUser, domain.User, error) {
	u, err := app.Users.GetUser(r.Context(), userID)
	if err != nil {
		return nil, domain.User{}, err
	}
	pks, err := app.Passkeys.ListByUser(r.Context(), userID)
	if err != nil {
		return nil, domain.User{}, err
	}
	creds := make([]webauthn.Credential, 0, len(pks))
	for _, p := range pks {
		var c webauthn.Credential
		if json.Unmarshal(p.CredentialJSON, &c) == nil {
			creds = append(creds, c)
		}
	}
	name := u.Username
	if name == "" {
		name = u.Email
	}
	display := u.DisplayName
	if display == "" {
		display = name
	}
	return &webAuthnUser{id: u.ID, name: name, display: display, creds: creds}, u, nil
}

// issueSessionForUser creates a session with the given auth method and sets the
// cookie. Mirrors the password-login path; shared by passkey login and invite
// signup.
func issueSessionForUser(w http.ResponseWriter, r *http.Request, app *App, userID domain.UserID, method domain.SessionAuthMethod, ttl time.Duration) error {
	token, err := randomSessionToken()
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now()
	expiresAt := now.Add(ttl)
	if _, err := app.Sessions.Create(r.Context(), domain.Session{
		UserID:           userID,
		TokenHash:        hash[:],
		AuthMethod:       method,
		UserAgentSummary: truncateUA(r.UserAgent(), 200),
		CreatedAt:        now,
		LastSeenAt:       now,
		ExpiresAt:        expiresAt,
	}); err != nil {
		return err
	}
	setSessionCookie(w, r, token, expiresAt)
	return nil
}

func newWebAuthn(publicURL string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(publicURL)
	if err != nil {
		return nil, err
	}
	origin := strings.TrimRight(publicURL, "/")
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(), // e.g. "localhost" or "cairn.example.com"
		RPDisplayName: "Cairn",
		RPOrigins:     []string{origin},
	})
}

func storeWASession(w http.ResponseWriter, r *http.Request, name string, sd *webauthn.SessionData) {
	b, _ := json.Marshal(sd)
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: base64.RawURLEncoding.EncodeToString(b),
		Path: "/auth/webauthn/", HttpOnly: true, Secure: isHTTPS(r),
		SameSite: http.SameSiteLaxMode, MaxAge: 300,
	})
}

func loadWASession(r *http.Request, name string) (*webauthn.SessionData, error) {
	c, err := r.Cookie(name)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil, err
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

func clearWACookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/auth/webauthn/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
