package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// blobExt returns the file extension (incl. dot) from an archived blob key,
// or "" if none — used for the download filename.
func blobExt(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return key[i:]
	}
	return ""
}

// mountActivitySources wires the user-facing per-source actions on an activity.
// Detach lets a user undo a wrong heuristic source-attach (Cairn's stage-2
// dedup occasionally pairs two distinct workouts); the source stays in storage
// for audit but no longer contributes to the merge. After detaching, the
// activity re-merges from its remaining sources — or soft-deletes itself if
// that was the last one.
//
//	POST /api/activities/{id}/sources/{sourceId}/detach   { reason? }
func mountActivitySources(mux *http.ServeMux, app *App, logger *slog.Logger) {
	// View the source's normalized (parsed) payload as stored.
	mux.HandleFunc("GET /api/activities/{id}/sources/{sourceId}/parsed", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		activityID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		sourceID, err := uuid.Parse(r.PathValue("sourceId"))
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(activityID))
		if err != nil || act.UserID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		src, err := app.Activities.GetSource(r.Context(), domain.SourceID(sourceID))
		if err != nil || src.ActivityID != domain.ActivityID(activityID) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, err := app.Activities.GetSourceParsedRaw(r.Context(), domain.SourceID(sourceID))
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})

	mux.HandleFunc("POST /api/activities/{id}/sources/{sourceId}/detach", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}

		activityID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		sourceID, err := uuid.Parse(r.PathValue("sourceId"))
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		aID := domain.ActivityID(activityID)
		sID := domain.SourceID(sourceID)

		// Ownership: the activity must belong to the caller and not be deleted.
		act, err := app.Activities.GetActivity(r.Context(), aID)
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// The source must belong to THIS activity (defence against detaching a
		// source from another of the user's activities by guessing its id).
		src, err := app.Activities.GetSource(r.Context(), sID)
		if err != nil || src.ActivityID != aID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			reason = "user_detach"
		}

		if err := app.Activities.DetachSource(r.Context(), sID, reason, time.Now().UTC()); err != nil {
			logger.Error("detach source failed", "source_id", sID, "error", err)
			http.Error(w, "detach failed", http.StatusInternalServerError)
			return
		}

		// Make the detach DURABLE (Gap 6): record the source's identity on the
		// denylist so a provider re-push doesn't silently re-attach it. The
		// ingest dedup path consults this before heuristic attach.
		if app.SourceDenylist != nil {
			if err := app.SourceDenylist.Add(r.Context(), domain.SourceDenylistEntry{
				UserID:            userID,
				Provider:          src.Provider,
				ExternalAccountID: src.ExternalAccountID,
				ExternalID:        src.ExternalID,
				Reason:            reason,
			}); err != nil {
				logger.Warn("detach denylist write failed", "source_id", sID, "error", err)
			}
		}

		// Re-merge from the remaining sources (or soft-delete if none remain).
		result, err := app.RecomputeActivity.Execute(r.Context(), aID)
		if err != nil {
			// The detach is durable; recompute is best-effort (matches the
			// pipeline-wide "auto-triggers log, don't fail the request" rule).
			logger.Warn("detach succeeded but recompute failed", "activity_id", aID, "error", err)
			writeJSON(w, http.StatusOK, map[string]any{
				"detached":        true,
				"recomputed":      false,
				"recompute_error": err.Error(),
				"activity_id":     aID.String(),
			})
			return
		}

		// If that was the activity's last source, it's gone — tell remote
		// followers so the federated copy is removed too.
		if result.SoftDeleted {
			publishActivityDelete(r.Context(), app, logger, act.UserID, aID)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"detached":     true,
			"recomputed":   true,
			"soft_deleted": result.SoftDeleted,
			"source_count": len(result.SourceIDs),
			"activity_id":  aID.String(),
		})
	})

	// Download the archived original file for a source. Ownership-checked;
	// redirects (302) to a short-lived presigned URL so the bytes stream from
	// object storage directly, not through this server.
	//
	//	GET /api/activities/{id}/sources/{sourceId}/download
	mux.HandleFunc("GET /api/activities/{id}/sources/{sourceId}/download", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if app.BlobStore == nil {
			http.Error(w, "object storage is not configured", http.StatusServiceUnavailable)
			return
		}
		activityID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		sourceID, err := uuid.Parse(r.PathValue("sourceId"))
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(activityID))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		src, err := app.Activities.GetSource(r.Context(), domain.SourceID(sourceID))
		if err != nil || src.ActivityID != domain.ActivityID(activityID) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if src.RawBlobID == "" {
			http.Error(w, "no archived original for this source", http.StatusNotFound)
			return
		}
		// Proxy the bytes through the server (it has S3 credentials and network
		// reach to the object store). Avoids depending on the store's endpoint
		// being browser-reachable; fine for the small raw activity files.
		data, ct, err := app.BlobStore.Get(r.Context(), src.RawBlobID)
		if err != nil {
			logger.Error("blob get failed", "source_id", sourceID, "error", err)
			http.Error(w, "download unavailable", http.StatusBadGateway)
			return
		}
		if ct == "" {
			ct = src.RawContentType
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		ext := blobExt(src.RawBlobID)
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", "attachment; filename=\""+src.Provider+"-"+src.ExternalID+ext+"\"")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	})

	// Re-fetch a source fresh from its external provider. Publishes a
	// fetch_source job for the source's (provider, account, ext_id); a worker
	// pulls it, re-fetches from the provider API, and the result flows through
	// the normal ingest pipeline → Stage-1 finds the existing source →
	// handleReimport replaces the payload + re-merges. Sets reimport_status to
	// 'updating' so the UI shows the in-flight state; the ingest resets it to
	// 'current' on the result.
	//
	// Re-fetch needs: an external account (not a manual upload) + a connected
	// worker for the provider + a valid OAuth token + provider budget. We can't
	// pre-check the latter two, so failures surface as the job sitting in the
	// queue / the worker NAK'ing — the source simply stays 'updating'.
	//
	//	POST /api/activities/{id}/sources/{sourceId}/reimport
	mux.HandleFunc("POST /api/activities/{id}/sources/{sourceId}/reimport", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if app.NATSBus == nil {
			http.Error(w, "re-fetch requires the async worker bus, which isn't configured", http.StatusServiceUnavailable)
			return
		}
		activityID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		sourceID, err := uuid.Parse(r.PathValue("sourceId"))
		if err != nil {
			http.Error(w, "bad source id", http.StatusBadRequest)
			return
		}
		aID := domain.ActivityID(activityID)
		sID := domain.SourceID(sourceID)

		act, err := app.Activities.GetActivity(r.Context(), aID)
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		src, err := app.Activities.GetSource(r.Context(), sID)
		if err != nil || src.ActivityID != aID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// mode: "refetch" (default) = fresh from provider; "reparse" =
		// re-run the worker's mapping over the archived raw blob (no provider
		// API quota). Reparse needs an archived blob + object storage.
		var reqBody struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		mode := reqBody.Mode
		if mode == "" {
			mode = "refetch"
		}

		if mode == "reparse" {
			if err := dispatchReparse(r.Context(), app, logger, userID, src); err != nil {
				writeReimportError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"queued": true, "mode": "reparse", "provider": src.Provider})
			return
		}

		if src.ExternalAccountID == nil || src.ExternalID == "" {
			// Manual uploads have no provider to re-fetch from.
			http.Error(w, "this source has no external provider to re-fetch from", http.StatusBadRequest)
			return
		}

		// Flip to 'updating' first so a concurrent view sees the in-flight state.
		if err := app.Activities.SetSourceReimportStatus(r.Context(), sID, domain.ReimportStatusUpdating, "user_refetch"); err != nil {
			logger.Error("set reimport status failed", "source_id", sID, "error", err)
			http.Error(w, "could not start re-fetch", http.StatusInternalServerError)
			return
		}

		body, _ := json.Marshal(map[string]any{
			"job_id":        "refetch:" + sID.String(),
			"account_id":    src.ExternalAccountID.String(),
			"user_id":       userID.String(),
			"provider":      src.Provider,
			"ext_id":        src.ExternalID,
			"fetch_streams": true,
			"reason":        "user_refetch",
		})
		// Deterministic msg-id collapses double-clicks within the dedup window.
		msgID := "refetch:" + src.Provider + ":" + src.ExternalID
		if err := app.NATSBus.Publish(r.Context(), "cairn.jobs.fetch_source."+src.Provider, msgID, body); err != nil {
			// Roll the status back so the user can retry; the job never landed.
			_ = app.Activities.SetSourceReimportStatus(r.Context(), sID, domain.ReimportStatusCurrent, "")
			logger.Error("publish fetch_source failed", "source_id", sID, "error", err)
			http.Error(w, "could not queue re-fetch", http.StatusBadGateway)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"queued": true, "mode": "refetch", "provider": src.Provider})
	})

	logger.Info("activity source actions mounted")
}

// reimportError carries an HTTP status alongside a message so the reparse
// dispatch helper can signal precondition failures (no blob / no storage)
// distinctly from internal errors.
type reimportError struct {
	status int
	msg    string
}

func (e *reimportError) Error() string { return e.msg }

func writeReimportError(w http.ResponseWriter, err error) {
	if re, ok := err.(*reimportError); ok {
		http.Error(w, re.msg, re.status)
		return
	}
	http.Error(w, "re-parse failed", http.StatusInternalServerError)
}

// dispatchReparse mints a presigned download URL for the source's archived raw
// blob and publishes a parse_blob.<provider> job. A worker re-runs its mapping
// over the archive (no provider API quota) and publishes a result that flows
// through the normal ingest pipeline → handleReimport replaces the payload.
func dispatchReparse(ctx context.Context, app *App, logger *slog.Logger, userID domain.UserID, src domain.ActivitySource) error {
	if app.BlobStore == nil {
		return &reimportError{http.StatusServiceUnavailable, "object storage isn't configured, so re-parse is unavailable"}
	}
	if src.RawBlobID == "" {
		return &reimportError{http.StatusBadRequest, "this source has no archived original to re-parse — re-fetch from the provider first"}
	}

	signed, err := app.BlobStore.PresignDownload(ctx, src.RawBlobID, port.PresignDownloadOpts{})
	if err != nil {
		logger.Error("presign download for reparse failed", "source_id", src.ID, "error", err)
		return &reimportError{http.StatusBadGateway, "could not prepare the archived blob for re-parse"}
	}

	var acctID string
	if src.ExternalAccountID != nil {
		acctID = src.ExternalAccountID.String()
	}
	job := map[string]any{
		"job_id":     "reparse:" + src.ID.String(),
		"source_id":  src.ID.String(),
		"user_id":    userID.String(),
		"provider":   src.Provider,
		"account_id": acctID,
		"ext_id":     src.ExternalID,
		"blob": map[string]any{
			"url":             signed.URL,
			"expires_at":      signed.ExpiresAt,
			"fallback_handle": src.RawBlobID, // worker re-presigns from this key if expired
		},
	}
	body, _ := json.Marshal(job)

	if err := app.Activities.SetSourceReimportStatus(ctx, src.ID, domain.ReimportStatusUpdating, "user_reparse"); err != nil {
		logger.Error("set reimport status failed", "source_id", src.ID, "error", err)
		return &reimportError{http.StatusInternalServerError, "could not start re-parse"}
	}
	msgID := "reparse:" + src.Provider + ":" + src.ExternalID
	if err := app.NATSBus.Publish(ctx, "cairn.jobs.parse_blob."+src.Provider, msgID, body); err != nil {
		_ = app.Activities.SetSourceReimportStatus(ctx, src.ID, domain.ReimportStatusCurrent, "")
		logger.Error("publish parse_blob failed", "source_id", src.ID, "error", err)
		return &reimportError{http.StatusBadGateway, "could not queue re-parse"}
	}
	return nil
}
