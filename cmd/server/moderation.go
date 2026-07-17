package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountModeration wires user blocking + content reporting + the admin
// moderation queue (multi-user v1).
//
//	POST   /api/users/{id}/block      block a user (severs follows both ways)
//	DELETE /api/users/{id}/block      unblock
//	GET    /api/blocks                list the users you've blocked
//	POST   /api/reports               file a content report
//	GET    /api/admin/reports         admin: moderation queue
//	POST   /api/admin/reports/{id}/resolve  admin: mark reviewed/dismissed
//	POST   /api/admin/activities/{id}/hide  admin: hide/unhide an activity
func mountModeration(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Blocks == nil {
		return
	}

	mux.HandleFunc("POST /api/users/{id}/block", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		target, err := domain.ParseUUID[domain.UserID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := app.Blocks.Block(r.Context(), me, target); err != nil {
			http.Error(w, "block failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blocked": true})
	})

	mux.HandleFunc("DELETE /api/users/{id}/block", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		target, err := domain.ParseUUID[domain.UserID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := app.Blocks.Unblock(r.Context(), me, target); err != nil {
			http.Error(w, "unblock failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blocked": false})
	})

	mux.HandleFunc("GET /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		me, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ids, err := app.Blocks.ListBlocked(r.Context(), me)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			if u, err := app.Users.GetUser(r.Context(), id); err == nil {
				out = append(out, userCardJSON(u))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	})

	if app.Reports != nil {
		mux.HandleFunc("POST /api/reports", func(w http.ResponseWriter, r *http.Request) {
			me, ok := resolveSessionUser(r, app)
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			var body struct {
				TargetKind string `json:"target_kind"`
				TargetID   string `json:"target_id"`
				Reason     string `json:"reason"`
			}
			if err := decodeJSONBody(r, &body); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			kind := domain.ReportTargetKind(body.TargetKind)
			if !kind.Valid() {
				http.Error(w, "bad target_kind", http.StatusBadRequest)
				return
			}
			tid, err := uuid.Parse(body.TargetID)
			if err != nil {
				http.Error(w, "bad target_id", http.StatusBadRequest)
				return
			}
			reason := strings.TrimSpace(body.Reason)
			if len(reason) > domain.MaxReportReasonLength {
				reason = reason[:domain.MaxReportReasonLength]
			}
			id, err := app.Reports.Create(r.Context(), domain.ContentReport{
				ReporterID: me, TargetKind: kind, TargetID: tid, Reason: reason,
			})
			if err != nil {
				http.Error(w, "report failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": id.String()})
		})

		mountModerationAdmin(mux, app, logger)
	}

	logger.Info("moderation (block + report) endpoints mounted")
}

// mountModerationAdmin wires the admin-only moderation queue. Gated via
// resolveAdminUser (session + admin role).
func mountModerationAdmin(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/admin/reports", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveModeratorUser(r, app); !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		status := domain.ReportStatus(r.URL.Query().Get("status"))
		reports, err := app.Reports.List(r.Context(), status,
			parseIntQuery(r, "limit", 50), parseIntQuery(r, "offset", 0))
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(reports))
		for _, rep := range reports {
			reporter := ""
			if u, err := app.Users.GetUser(r.Context(), rep.ReporterID); err == nil {
				reporter = u.Username
			}
			out = append(out, map[string]any{
				"id":          rep.ID.String(),
				"reporter":    reporter,
				"target_kind": string(rep.TargetKind),
				"target_id":   rep.TargetID.String(),
				"reason":      rep.Reason,
				"status":      string(rep.Status),
				"created_at":  rep.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"reports": out})
	})

	mux.HandleFunc("POST /api/admin/reports/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := resolveModeratorUser(r, app)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		id, err := domain.ParseUUID[domain.ContentReportID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body struct {
			Status string `json:"status"`
		}
		_ = decodeJSONBody(r, &body)
		status := domain.ReportStatus(body.Status)
		if status != domain.ReportReviewed && status != domain.ReportDismissed {
			status = domain.ReportReviewed
		}
		if err := app.Reports.UpdateStatus(r.Context(), id, status, admin.ID); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": string(status)})
	})

	// Role management is an admin action (delegated administration: an admin
	// promotes a trusted user to 'moderator' for the report queue).
	mux.HandleFunc("PUT /api/admin/users/{id}/role", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		target, err := domain.ParseUUID[domain.UserID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		role := domain.UserRole(body.Role)
		if !role.Valid() {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		if err := app.Users.UpdateUserRole(r.Context(), target, role); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"role": string(role)})
	})

	if app.Moderation != nil {
		mux.HandleFunc("POST /api/admin/activities/{id}/hide", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := resolveModeratorUser(r, app); !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			id, err := uuid.Parse(r.PathValue("id"))
			if err != nil {
				http.Error(w, "bad id", http.StatusBadRequest)
				return
			}
			var body struct {
				Hidden bool `json:"hidden"`
			}
			_ = decodeJSONBody(r, &body)
			if err := app.Moderation.SetActivityHidden(r.Context(), id, body.Hidden); err != nil {
				http.Error(w, "update failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"hidden": body.Hidden})
		})
	}
}
