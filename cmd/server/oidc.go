package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/usecase/auth"
)

// Cookie names. SESSION is the long-lived bearer; LOGIN_FLOW carries
// the per-attempt state+nonce+verifier between the /start redirect and
// the /callback POST. Both are HttpOnly+Secure+SameSite=Lax.
const (
	sessionCookieName   = "cairn_session"
	loginFlowCookieName = "cairn_login_flow"
)

// mountOIDCEndpoints wires the three URLs the OIDC flow uses:
//
//	GET  /auth/oidc/clients               → list configured providers (login page)
//	GET  /auth/oidc/{provider}/start      → redirect to IdP authorize
//	GET  /auth/oidc/callback              → exchange code, set session
//	POST /auth/logout                     → revoke session, clear cookie
//
// Providers come entirely from the environment (CAIRN_OIDC_*); there is no
// client database. Gated on app.OIDCLogin being non-nil — when no providers
// are configured the routes don't register and the frontend falls back to
// password login.
func mountOIDCEndpoints(mux *http.ServeMux, app *App, logger *slog.Logger) {
	// Session logout is auth-mechanism-agnostic (password, passkey, OIDC all
	// set the same session cookie) — mount it before the provider guard so it
	// exists even when no OIDC provider is configured.
	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && cookie.Value != "" {
			hash := sha256.Sum256([]byte(cookie.Value))
			if sess, err := app.Sessions.GetByTokenHash(r.Context(), hash[:]); err == nil {
				_ = app.Sessions.Revoke(r.Context(), sess.ID)
			}
		}
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	})

	if app.OIDCLogin == nil {
		logger.Info("oidc endpoints not mounted: no providers configured (logout still mounted)")
		return
	}

	mux.HandleFunc("GET /auth/oidc/clients", func(w http.ResponseWriter, r *http.Request) {
		type clientJSON struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		}
		providers := app.OIDCLogin.Providers()
		out := make([]clientJSON, len(providers))
		for i, p := range providers {
			out[i] = clientJSON{ID: p.ID, DisplayName: p.Name}
		}
		writeJSON(w, http.StatusOK, map[string]any{"clients": out})
	})

	mux.HandleFunc("GET /auth/oidc/{provider}/start", func(w http.ResponseWriter, r *http.Request) {
		providerID := r.PathValue("provider")

		out, err := app.OIDCLogin.StartLogin(r.Context(), providerID)
		if err != nil {
			logger.Warn("oidc start failed", "provider", providerID, "error", err)
			status := http.StatusBadGateway
			if errors.Is(err, auth.ErrProviderNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, "oidc start: "+err.Error(), status)
			return
		}

		flow := loginFlowState{
			Provider:     providerID,
			State:        out.State,
			Nonce:        out.Nonce,
			CodeVerifier: out.CodeVerifier,
			CreatedAt:    time.Now().Unix(),
		}
		if err := setLoginFlowCookie(w, r, app, flow); err != nil {
			http.Error(w, "cookie write: "+err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, out.AuthURL, http.StatusFound)
	})

	mux.HandleFunc("GET /auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			redirectLoginError(w, r, "oidc_failed")
			return
		}

		flow, err := readLoginFlowCookie(r, app)
		if err != nil {
			// Expired/missing/tampered flow cookie — the common "took too long"
			// or "came back to a different browser" case.
			redirectLoginError(w, r, "oidc_expired")
			return
		}
		// Always clear the flow cookie regardless of outcome.
		clearLoginFlowCookie(w, r)

		if state != flow.State {
			redirectLoginError(w, r, "oidc_state")
			return
		}
		if flow.Provider == "" {
			redirectLoginError(w, r, "oidc_failed")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		res, err := app.OIDCLogin.CompleteLogin(ctx, auth.CompleteLoginInput{
			ProviderID:    flow.Provider,
			Code:          code,
			CodeVerifier:  flow.CodeVerifier,
			ExpectedNonce: flow.Nonce,
			ClientIP:      clientIP(r),
			UserAgent:     r.UserAgent(),
		})
		if err != nil {
			logger.Warn("oidc callback failed", "error", err)
			redirectLoginError(w, r, oidcErrorCode(err))
			return
		}

		setSessionCookie(w, r, res.BearerToken, res.ExpiresAt)
		// Successful login lands the user on /. Frontends that want
		// post-login deep-links should encode them in `state` themselves
		// (round-tripped through the flow cookie).
		http.Redirect(w, r, "/", http.StatusFound)
	})

	logger.Info("oidc endpoints mounted", "paths", []string{
		"/auth/oidc/clients", "/auth/oidc/{provider}/start",
		"/auth/oidc/callback",
	})
}

// ---------------------------------------------------------------------------
// Cookies
// ---------------------------------------------------------------------------

// loginFlowState is what we encode into the cairn_login_flow cookie.
// It's not signed — the cookie is short-lived (5 min) and the state
// value is checked against the callback's state param anyway, so a
// tampered cookie just fails the state-mismatch check.
type loginFlowState struct {
	Provider     string `json:"provider"`
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

// loginFlowCookieAAD binds the encrypted flow cookie to its purpose.
const loginFlowCookieAAD = "cairn_login_flow"

const loginFlowCookieTTL = 10 * time.Minute

func setLoginFlowCookie(w http.ResponseWriter, r *http.Request, app *App, s loginFlowState) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Encrypt+authenticate the cookie so a planted/tampered flow (state, nonce,
	// PKCE verifier) is rejected — a forged cookie can't force a chosen nonce.
	if app.SecretBox != nil {
		ct, err := app.SecretBox.Encrypt(body, []byte(loginFlowCookieAAD))
		if err != nil {
			return err
		}
		body = ct
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginFlowCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(body),
		Path:     "/auth/oidc/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginFlowCookieTTL.Seconds()),
	})
	return nil
}

func readLoginFlowCookie(r *http.Request, app *App) (loginFlowState, error) {
	c, err := r.Cookie(loginFlowCookieName)
	if err != nil {
		return loginFlowState{}, err
	}
	body, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return loginFlowState{}, err
	}
	if app.SecretBox != nil {
		pt, err := app.SecretBox.Decrypt(body, []byte(loginFlowCookieAAD))
		if err != nil {
			return loginFlowState{}, fmt.Errorf("login flow cookie integrity check failed: %w", err)
		}
		body = pt
	}
	var s loginFlowState
	if err := json.Unmarshal(body, &s); err != nil {
		return loginFlowState{}, err
	}
	if time.Since(time.Unix(s.CreatedAt, 0)) > loginFlowCookieTTL {
		return loginFlowState{}, errors.New("login flow cookie expired")
	}
	return s, nil
}

func clearLoginFlowCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginFlowCookieName,
		Value:    "",
		Path:     "/auth/oidc/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	// Set both Expires and Max-Age. Max-Age takes precedence in modern browsers
	// and is immune to a wrong client clock; Expires is the legacy fallback.
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isHTTPS reports whether the request arrived over TLS — either directly
// or via a reverse-proxy that set X-Forwarded-Proto=https. The Secure
// cookie attribute follows this so dev (http://localhost:8080) doesn't
// drop the cookie.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

// oidcErrorCode maps a CompleteLogin failure to a stable login-page ?error=
// code (the SPA renders the friendly message via redirectLoginError, shared
// with the password flow — no raw plaintext dead-end, no internal leakage).
func oidcErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrProviderNotFound):
		return "oidc_unavailable"
	case errors.Is(err, auth.ErrAutoProvisionDenied):
		return "auto_provision_denied"
	case errors.Is(err, auth.ErrSubjectMissing), errors.Is(err, auth.ErrEmailMissing):
		return "oidc_bad_response"
	case errors.Is(err, auth.ErrIDTokenVerifyFailed):
		return "oidc_verify_failed"
	default:
		return "oidc_failed"
	}
}
