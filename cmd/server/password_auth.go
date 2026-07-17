package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountPasswordAuth registers the username/email + password sign-in
// endpoint that the SvelteKit /login page posts to.
//
//	POST /auth/password    form: identifier, password, [redirect]
//
// On success it issues a server-side session (AuthMethod=password), sets
// the cairn_session cookie — the same cookie the OIDC callback sets and
// the Connect-RPC SessionInterceptor verifies — and redirects to the
// post-login destination. On failure it bounces back to /login with a
// stable error code in the query string so the page can render a message
// without leaking which half of the credential pair was wrong.
//
// The reverse proxy (Caddy in the compose stack, vite in dev) forwards
// /auth/* to this server, so the form post and the resulting Set-Cookie
// are first-party with the rest of the app.
func mountPasswordAuth(mux *http.ServeMux, app *App, logger *slog.Logger, sessionTTL time.Duration) {
	mux.HandleFunc("POST /auth/password", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		identifier := strings.TrimSpace(strings.ToLower(r.PostFormValue("identifier")))
		password := r.PostFormValue("password")
		dest := safeRedirect(r.PostFormValue("redirect"))

		if identifier == "" || password == "" {
			redirectLoginError(w, r, "missing_credentials")
			return
		}

		ctx := r.Context()

		// Combined lookup: resolves either a username or an email and
		// returns the user plus its credentials. A miss still runs an
		// Argon2id round below so present/absent users take ~equal time.
		user, cred, err := app.Users.GetCredentialsByLogin(ctx, identifier)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				_, _ = app.PasswordHasher.Verify(password, "")
				redirectLoginError(w, r, "invalid_credentials")
				return
			}
			logger.Error("password login: credentials lookup failed", "error", err)
			redirectLoginError(w, r, "server_error")
			return
		}

		ok, err := app.PasswordHasher.Verify(password, cred.PasswordHash)
		if err != nil {
			logger.Error("password login: verify failed", "error", err, "user_id", user.ID)
			redirectLoginError(w, r, "server_error")
			return
		}
		if !ok {
			redirectLoginError(w, r, "invalid_credentials")
			return
		}
		if !user.IsActive() {
			redirectLoginError(w, r, "account_inactive")
			return
		}

		// Issue the session. Plaintext token goes in the cookie; only its
		// SHA-256 is persisted — mirrors the OIDC issueSession path.
		token, err := randomSessionToken()
		if err != nil {
			logger.Error("password login: token generation failed", "error", err)
			redirectLoginError(w, r, "server_error")
			return
		}
		hash := sha256.Sum256([]byte(token))
		now := time.Now()
		expiresAt := now.Add(sessionTTL)

		if _, err := app.Sessions.Create(ctx, domain.Session{
			UserID:           user.ID,
			TokenHash:        hash[:],
			AuthMethod:       domain.SessionAuthPassword,
			UserAgentSummary: truncateUA(r.UserAgent(), 200),
			CreatedAt:        now,
			LastSeenAt:       now,
			ExpiresAt:        expiresAt,
		}); err != nil {
			logger.Error("password login: create session failed", "error", err, "user_id", user.ID)
			redirectLoginError(w, r, "server_error")
			return
		}

		setSessionCookie(w, r, token, expiresAt)
		logger.Info("password login succeeded", "user_id", user.ID, "username", user.Username)
		http.Redirect(w, r, dest, http.StatusSeeOther)
	})

	logger.Info("password auth endpoint mounted", "path", "/auth/password")
}

// safeRedirect constrains the post-login destination to a local path so
// the redirect form field can't be abused as an open redirect. Anything
// that isn't a single-slash-rooted relative path collapses to "/".
func safeRedirect(dest string) string {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		return "/"
	}
	return dest
}

func redirectLoginError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?error="+code, http.StatusSeeOther)
}

func randomSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func truncateUA(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
