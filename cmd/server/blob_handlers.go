package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/port"
)

// startBlobHandlers wires the NATS request/reply subjects workers call to
// obtain presigned blob URLs. Two wildcards:
//
//	cairn.blobs.presign_upload.>     → server mints a PUT URL
//	cairn.blobs.presign_download.>   → server mints a GET URL
//
// The trailing token is the provider name (informational — the actual
// blob key is server-derived, so a strava-worker can't impersonate a
// garmin-worker beyond what NATS subject permissions allow).
//
// Gated on app.BlobStore + app.NATSBus. When either is nil the handlers
// are not started (workers' presign requests time out, which is the
// correct signal).
func startBlobHandlers(ctx context.Context, app *App, logger *slog.Logger) ([]port.Subscription, error) {
	if app.NATSBus == nil || app.BlobStore == nil {
		if app.NATSBus == nil {
			logger.Info("blob handlers not started: no NATS bus")
		} else {
			logger.Info("blob handlers not started: no blob store configured")
		}
		return nil, nil
	}

	log := logger.With("component", "blob_handler")
	subs := make([]port.Subscription, 0, 2)

	upload, err := app.NATSBus.RespondTo(ctx,
		"cairn.blobs.presign_upload.>",
		func(ctx context.Context, body []byte) ([]byte, error) {
			return handlePresignUpload(ctx, app, log, body)
		})
	if err != nil {
		return nil, fmt.Errorf("subscribe presign_upload: %w", err)
	}
	subs = append(subs, upload)
	log.Info("subscribed", "subject", "cairn.blobs.presign_upload.>")

	download, err := app.NATSBus.RespondTo(ctx,
		"cairn.blobs.presign_download.>",
		func(ctx context.Context, body []byte) ([]byte, error) {
			return handlePresignDownload(ctx, app, log, body)
		})
	if err != nil {
		_ = upload.Close(context.Background())
		return nil, fmt.Errorf("subscribe presign_download: %w", err)
	}
	subs = append(subs, download)
	log.Info("subscribed", "subject", "cairn.blobs.presign_download.>")

	return subs, nil
}

// presignUploadReq is the wire-format request the workersdk publishes.
// Field names match workersdk.PresignUploadRequest verbatim — the two
// types are deliberately decoupled (different deploy units) so we
// re-declare here rather than import.
type presignUploadReq struct {
	SourceID string `json:"source_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	Provider string `json:"provider,omitempty"` // worker should set; informational
	// Kind selects the key prefix. "" / "raw" → durable raw-archive layout;
	// "result" → transfer/ (claim-checked JobResult bodies, short-lived:
	// deleted after ingest, lifecycle-reaped as orphans).
	Kind          string `json:"kind,omitempty"`
	ContentType   string `json:"content_type"`
	ContentLength int64  `json:"content_length"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

type presignDownloadReq struct {
	BlobID string `json:"blob_id,omitempty"`
	Handle string `json:"handle,omitempty"`
}

// presignResp matches workersdk.PresignedURL.
type presignResp struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	BlobID          string            `json:"blob_id,omitempty"`
	ExpiresAt       time.Time         `json:"expires_at"`
	RequiredHeaders map[string]string `json:"required_headers,omitempty"`
}

func handlePresignUpload(ctx context.Context, app *App, log *slog.Logger, body []byte) ([]byte, error) {
	var req presignUploadReq
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warn("decode presign_upload failed", "error", err)
		return nil, err
	}
	if req.ContentType == "" {
		return nil, errors.New("content_type required")
	}

	provider := req.Provider
	if provider == "" {
		provider = "unknown"
	}
	blobID := uuid.New().String()
	var key string
	if req.Kind == "result" {
		key = transferKey(provider, blobID)
	} else {
		key = blobKey(provider, req.UserID, req.SourceID, blobID)
	}

	// ±10% of declared content-length is a reasonable defensive bound.
	// A 0 declared length disables the upper bound so we don't reject
	// workers that genuinely don't know the size upfront.
	var minLen, maxLen int64
	if req.ContentLength > 0 {
		minLen = req.ContentLength * 90 / 100
		maxLen = req.ContentLength * 110 / 100
	}

	signed, err := app.BlobStore.PresignUpload(ctx, key, port.PresignUploadOpts{
		ContentType:      req.ContentType,
		ContentLengthMin: minLen,
		ContentLengthMax: maxLen,
		ContentSHA256:    req.ContentSHA256,
	})
	if err != nil {
		log.Error("presign upload failed", "key", key, "error", err)
		return nil, err
	}

	resp := presignResp{
		Method: signed.Method,
		URL:    signed.URL,
		// Return the FULL S3 key (not the bare uuid) as the blob id. The worker
		// stores this verbatim as raw_blob_id, and BlobStore.Get /
		// presign_download both resolve a blob by its full key — so an archived
		// blob round-trips (download + parse_blob). Returning the bare uuid here
		// was a latent bug: the object lives at blobKey(...), not at "<uuid>".
		BlobID:          key,
		ExpiresAt:       signed.ExpiresAt,
		RequiredHeaders: signed.RequiredHeaders,
	}
	return json.Marshal(resp)
}

func handlePresignDownload(ctx context.Context, app *App, log *slog.Logger, body []byte) ([]byte, error) {
	var req presignDownloadReq
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warn("decode presign_download failed", "error", err)
		return nil, err
	}
	if req.BlobID == "" {
		return nil, errors.New("blob_id required")
	}

	// The blob_id is server-generated at upload time; the key was built
	// from (provider, user, source, blob_id). For download we accept
	// blob_id alone — the server resolves the full key from the source
	// row's raw_blob_id when integrated; for now we accept the legacy
	// shape "<provider>/<blob_id>" if the caller passed a slashed path.
	key := req.BlobID

	signed, err := app.BlobStore.PresignDownload(ctx, key, port.PresignDownloadOpts{})
	if err != nil {
		log.Error("presign download failed", "key", key, "error", err)
		return nil, err
	}

	resp := presignResp{
		Method:          signed.Method,
		URL:             signed.URL,
		BlobID:          req.BlobID,
		ExpiresAt:       signed.ExpiresAt,
		RequiredHeaders: signed.RequiredHeaders,
	}
	return json.Marshal(resp)
}

// blobKey produces the canonical S3 key. Layout puts user folder first
// so per-user purges (DSGVO delete, account disconnect) can use the
// S3 batch-delete-by-prefix path.
//
//	users/<user>/<provider>/sources/<source>/<blob>
//
// When user or source are empty (e.g. manual uploads pre-auth or
// admin-triggered blobs) we fall back to a shared bucket prefix.
// transferPrefix is where claim-checked result payloads live; the bucket
// lifecycle rule (wire.go) expires this prefix as the orphan backstop.
const transferPrefix = "transfer/"

func transferKey(provider, blobID string) string {
	return fmt.Sprintf("%s%s/%s.json", transferPrefix, provider, blobID)
}

func blobKey(provider, userID, sourceID, blobID string) string {
	switch {
	case userID != "" && sourceID != "":
		return fmt.Sprintf("users/%s/%s/sources/%s/%s", userID, provider, sourceID, blobID)
	case userID != "":
		return fmt.Sprintf("users/%s/%s/uploads/%s", userID, provider, blobID)
	default:
		return fmt.Sprintf("shared/%s/%s", provider, blobID)
	}
}
