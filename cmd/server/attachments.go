package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountAttachmentEndpoints registers activity attachment (photo) routes:
//
//	GET    /api/activities/{id}/attachments              list metadata
//	POST   /api/activities/{id}/attachments              upload (multipart "file")
//	GET    /api/activities/{id}/attachments/{aid}/raw    stream the image bytes
//	DELETE /api/activities/{id}/attachments/{aid}        remove
//
// Reads (list, raw) are visibility-gated: the owner always sees them; a
// non-owner sees them when the CategoryPhotos gate is open (like map.png).
// Writes (upload, delete) are owner-only. Bytes live in the BlobStore; the
// server proxies them on /raw so the storage endpoint never leaks to the browser.
func mountAttachmentEndpoints(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Attachments == nil {
		return
	}

	// authOwner: the activity must exist and belong to the caller (writes).
	authOwner := func(w http.ResponseWriter, r *http.Request) (domain.ActivityID, bool) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return domain.ActivityID{}, false
		}
		aid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid activity id", http.StatusBadRequest)
			return domain.ActivityID{}, false
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(aid))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return domain.ActivityID{}, false
		}
		return domain.ActivityID(aid), true
	}

	// authView: owner sees photos; a non-owner sees them only when the activity
	// is non-private and the CategoryPhotos gate is open for them (mirrors the
	// map.png visibility re-check). Reads only.
	authView := func(w http.ResponseWriter, r *http.Request) (domain.ActivityID, bool) {
		aid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid activity id", http.StatusBadRequest)
			return domain.ActivityID{}, false
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(aid))
		if err != nil || act.IsDeleted() {
			http.NotFound(w, r)
			return domain.ActivityID{}, false
		}
		var viewer *domain.UserID
		if v, ok := optionalSessionUser(r, app); ok {
			viewer = &v
		}
		if viewer != nil && *viewer == act.UserID {
			return act.ID, true // owner
		}
		if act.Privacy == domain.PrivacyPrivate || act.HiddenByAdmin {
			http.NotFound(w, r)
			return domain.ActivityID{}, false
		}
		cats, _ := visibilityFor(r.Context(), app, act.UserID, viewer, act.ID, false)
		if !cats.Has(domain.CategoryPhotos) {
			http.NotFound(w, r)
			return domain.ActivityID{}, false
		}
		return act.ID, true
	}

	mux.HandleFunc("GET /api/activities/{id}/attachments", func(w http.ResponseWriter, r *http.Request) {
		activityID, ok := authView(w, r)
		if !ok {
			return
		}
		atts, err := app.Attachments.ListForActivity(r.Context(), activityID)
		if err != nil {
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(atts))
		for _, a := range atts {
			out = append(out, map[string]any{
				"id":           a.ID.String(),
				"url":          fmt.Sprintf("/api/activities/%s/attachments/%s/raw", activityID.String(), a.ID.String()),
				"caption":      a.Caption,
				"content_type": a.ContentType,
				"width":        a.Width,
				"height":       a.Height,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"attachments": out})
	})

	mux.HandleFunc("GET /api/activities/{id}/attachments/{aid}/raw", func(w http.ResponseWriter, r *http.Request) {
		activityID, ok := authView(w, r)
		if !ok {
			return
		}
		if app.BlobStore == nil {
			http.Error(w, "storage not configured", http.StatusServiceUnavailable)
			return
		}
		aid, err := uuid.Parse(r.PathValue("aid"))
		if err != nil {
			http.Error(w, "invalid attachment id", http.StatusBadRequest)
			return
		}
		att, err := app.Attachments.Get(r.Context(), domain.AttachmentID(aid))
		if err != nil || att.ActivityID != activityID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		data, ct, err := app.BlobStore.Get(r.Context(), att.BlobID)
		if err != nil {
			http.Error(w, "blob unavailable", http.StatusBadGateway)
			return
		}
		if att.ContentType != "" {
			ct = att.ContentType
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("POST /api/activities/{id}/attachments", func(w http.ResponseWriter, r *http.Request) {
		activityID, ok := authOwner(w, r)
		if !ok {
			return
		}
		userID, _ := resolveSessionUser(r, app)
		if app.BlobStore == nil {
			http.Error(w, "storage not configured", http.StatusServiceUnavailable)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad multipart form", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, 32<<20))
		if err != nil {
			http.Error(w, "read file failed", http.StatusBadRequest)
			return
		}
		ct := http.DetectContentType(raw)
		if !strings.HasPrefix(ct, "image/") {
			http.Error(w, "only image uploads are supported", http.StatusBadRequest)
			return
		}
		key := fmt.Sprintf("attachments/%s/%s/%s", userID.String(), activityID.String(), uuid.NewString())
		if err := app.BlobStore.Put(r.Context(), key, raw, ct); err != nil {
			logger.Error("attachment upload: blob put", "error", err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		att := domain.Attachment{
			ActivityID:  activityID,
			UserID:      userID,
			BlobID:      key,
			ContentType: ct,
			Caption:     strings.TrimSpace(r.FormValue("caption")),
		}
		if err := app.Attachments.Add(r.Context(), att); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("DELETE /api/activities/{id}/attachments/{aid}", func(w http.ResponseWriter, r *http.Request) {
		activityID, ok := authOwner(w, r)
		if !ok {
			return
		}
		aid, err := uuid.Parse(r.PathValue("aid"))
		if err != nil {
			http.Error(w, "invalid attachment id", http.StatusBadRequest)
			return
		}
		att, err := app.Attachments.Get(r.Context(), domain.AttachmentID(aid))
		if err != nil || att.ActivityID != activityID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		blobID, err := app.Attachments.Delete(r.Context(), domain.AttachmentID(aid))
		if err != nil {
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		if app.BlobStore != nil && blobID != "" {
			if err := app.BlobStore.Delete(r.Context(), blobID); err != nil {
				logger.Warn("attachment delete: blob drop failed", "blob_id", blobID, "error", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	logger.Info("attachment endpoints mounted", "path", "/api/activities/{id}/attachments")
}
