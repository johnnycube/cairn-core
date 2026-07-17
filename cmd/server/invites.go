package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/usecase/invite"
)

// mountInvites wires the signup-invite endpoints:
//
//	POST   /api/admin/invites          (admin) → mint a code (shown once)
//	GET    /api/admin/invites          (admin) → list invites
//	POST   /api/admin/invites/{id}/revoke (admin)
//	POST   /auth/invite/redeem                → create a user from a code + log in
func mountInvites(mux *http.ServeMux, app *App, logger *slog.Logger, publicURL string, sessionTTL time.Duration) {
	if app.CreateInvite == nil || app.RedeemInvite == nil || app.Invites == nil {
		return
	}

	// --- admin: create ----------------------------------------------------
	mux.HandleFunc("POST /api/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		adminUser, ok := resolveAdminUser(r, app)
		if !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Role          string `json:"role"`
			Email         string `json:"email"`
			ExpiresInDays int    `json:"expires_in_days"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		role := domain.UserRole(body.Role)
		if role == "" {
			role = domain.UserRoleUser
		}
		var expiresIn time.Duration
		if body.ExpiresInDays > 0 {
			expiresIn = time.Duration(body.ExpiresInDays) * 24 * time.Hour
		}
		createdBy := adminUser.ID
		res, err := app.CreateInvite.Execute(r.Context(), invite.CreateInviteInput{
			Role: role, Email: strings.TrimSpace(body.Email), ExpiresIn: expiresIn, CreatedBy: &createdBy,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var exp any
		if res.ExpiresAt != nil {
			exp = res.ExpiresAt.UTC().Format(time.RFC3339)
		}
		// The plaintext code (and a ready-to-share signup link) are returned
		// ONCE here and never again.
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": res.ID.String(), "code": res.Code, "prefix": res.Prefix,
			"expires_at": exp,
			"signup_url": strings.TrimRight(publicURL, "/") + "/signup?code=" + res.Code,
		})
	})

	// --- admin: list ------------------------------------------------------
	mux.HandleFunc("GET /api/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		invs, err := app.Invites.ListForInstance(r.Context())
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		now := time.Now()
		out := make([]map[string]any, 0, len(invs))
		for _, i := range invs {
			out = append(out, map[string]any{
				"id": i.ID.String(), "prefix": i.CodePrefix, "email": i.Email,
				"role": string(i.AssignedRole), "status": i.Status(now),
				"created_at": i.CreatedAt.UTC().Format(time.RFC3339),
				"expires_at": fmtTimePtr(i.ExpiresAt),
				"used_at":    fmtTimePtr(i.UsedAt),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"invites": out})
	})

	// --- admin: revoke ----------------------------------------------------
	mux.HandleFunc("POST /api/admin/invites/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := app.Invites.Revoke(r.Context(), domain.InviteID(id), time.Now().UTC()); err != nil {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// --- public: redeem ---------------------------------------------------
	mux.HandleFunc("POST /auth/invite/redeem", func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Code        string `json:"code"`
			Username    string `json:"username"`
			Email       string `json:"email"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		user, err := app.RedeemInvite.Execute(r.Context(), invite.RedeemInviteInput{
			Code:        strings.TrimSpace(body.Code),
			Username:    body.Username,
			Email:       body.Email,
			Password:    body.Password,
			DisplayName: body.DisplayName,
		})
		if err != nil {
			if errors.Is(err, domain.ErrInviteInvalid) {
				http.Error(w, "invalid or already-used invite code", http.StatusBadRequest)
				return
			}
			// User-shape / uniqueness errors are safe to surface verbatim.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Log the new user straight in.
		if err := issueSessionForUser(w, r, app, user.ID, domain.SessionAuthPassword, sessionTTL); err != nil {
			logger.Error("invite redeem: issue session failed", "error", err)
			http.Error(w, "account created but session failed; please sign in", http.StatusInternalServerError)
			return
		}
		// Kick off email verification for self-entered (not invite-pinned) emails.
		if !user.EmailVerified && app.Email != nil && user.Email != "" {
			if err := sendVerificationEmail(r.Context(), app, logger, strings.TrimRight(publicURL, "/"), user); err != nil {
				logger.Warn("invite redeem: verification email send failed", "error", err)
			}
		}
		logger.Info("invite redeemed", "user_id", user.ID, "username", user.Username)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email_verified": user.EmailVerified})
	})

	logger.Info("invite endpoints mounted")
}

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
