package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountPATs exposes personal-access-token management (session-authed):
//
//	GET    /api/pats          → list (never returns the token)
//	POST   /api/pats          { name, expires_in_days? } → create (token shown once)
//	DELETE /api/pats/{id}     → revoke
//
// PATs authenticate API/CLI clients as their owner via
// `Authorization: Bearer cairn_pat_...` (resolved in resolveSessionUser).
func mountPATs(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.PATs == nil {
		return
	}

	mux.HandleFunc("GET /api/pats", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		list, err := app.PATs.ListForUser(r.Context(), userID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		now := time.Now()
		out := make([]map[string]any, 0, len(list))
		for _, p := range list {
			status := "active"
			if p.RevokedAt != nil {
				status = "revoked"
			} else if !p.IsValidAt(now) {
				status = "expired"
			}
			out = append(out, map[string]any{
				"id": p.ID.String(), "name": p.Name, "prefix": p.TokenPrefix, "status": status,
				"created_at":   p.CreatedAt.UTC().Format(time.RFC3339),
				"expires_at":   fmtTimePtr(p.ExpiresAt),
				"last_used_at": fmtTimePtr(p.LastUsedAt),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
	})

	mux.HandleFunc("POST /api/pats", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Name          string `json:"name"`
			ExpiresInDays int    `json:"expires_in_days"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		// cairn_pat_<32 random url-safe chars>. The prefix marks it for the
		// auth resolver; only the hash is stored.
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			http.Error(w, "token generation failed", http.StatusInternalServerError)
			return
		}
		token := patTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
		hash := sha256.Sum256([]byte(token))

		var expiresAt *time.Time
		if body.ExpiresInDays > 0 {
			t := time.Now().UTC().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
			expiresAt = &t
		}
		id, err := app.PATs.Create(r.Context(), domain.PersonalAccessToken{
			UserID: userID, Name: name, TokenPrefix: token[:len(patTokenPrefix)+6] + "…", ExpiresAt: expiresAt,
		}, hash[:])
		if err != nil {
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		// token shown exactly once
		writeJSON(w, http.StatusCreated, map[string]any{"id": id.String(), "token": token})
	})

	mux.HandleFunc("DELETE /api/pats/{id}", func(w http.ResponseWriter, r *http.Request) {
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
		if err := app.PATs.Revoke(r.Context(), userID, domain.PATID(id)); err != nil {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("personal access token endpoints mounted")
}
