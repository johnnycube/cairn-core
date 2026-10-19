package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/export"
)

// mountActivityExport serves the merged activity as a standard interchange
// file (GPX / TCX), generated from Cairn's own merged stream + summary — so it
// works regardless of the original provider/format. Distinct from the
// per-source "download original" which streams the archived raw blob verbatim.
//
// The optional ?exclude=gps,heart_rate,… query drops features from the file
// (tokens from export.OptionsFromExclude); the options endpoint tells the UI
// which features this activity actually has.
//
//	GET /api/activities/{id}/export.gpx
//	GET /api/activities/{id}/export.tcx
//	GET /api/activities/{id}/export/options
func mountActivityExport(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/activities/{id}/export.gpx", activityExportFileHandler(app, logger, "gpx"))
	mux.HandleFunc("GET /api/activities/{id}/export.tcx", activityExportFileHandler(app, logger, "tcx"))
	mux.HandleFunc("GET /api/activities/{id}/export.fit", activityExportFileHandler(app, logger, "fit"))

	mux.HandleFunc("GET /api/activities/{id}/export/options", func(w http.ResponseWriter, r *http.Request) {
		act, stream, ok := activityExportLoad(app, logger, w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available": export.Available(act, stream),
		})
	})

	logger.Info("activity export endpoints mounted")
}

// activityExportLoad resolves auth + activity + raw merged stream; on failure it
// has already written the HTTP error.
func activityExportLoad(app *App, logger *slog.Logger, w http.ResponseWriter, r *http.Request) (domain.Activity, domain.Stream, bool) {
	var none domain.Activity
	userID, ok := resolveSessionUser(r, app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return none, domain.Stream{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad activity id", http.StatusBadRequest)
		return none, domain.Stream{}, false
	}
	act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(id))
	if err != nil || act.UserID != userID || act.IsDeleted() {
		http.Error(w, "not found", http.StatusNotFound)
		return none, domain.Stream{}, false
	}
	if act.PrimaryStreamSourceID == nil {
		http.Error(w, "this activity has no stream to export", http.StatusBadRequest)
		return none, domain.Stream{}, false
	}

	stream, err := app.Streams.QueryStream(r.Context(), domain.StreamQuery{
		ActivitySourceID: *act.PrimaryStreamSourceID,
		Channels:         nil, // all channels
		Resolution:       domain.StreamResolutionRaw,
	})
	if err != nil {
		logger.Error("export: stream query failed", "activity", act.ID, "error", err)
		http.Error(w, "stream query failed", http.StatusInternalServerError)
		return none, domain.Stream{}, false
	}
	return act, stream, true
}

// activityExportEncode renders the file in format; on failure it has already written the HTTP error.
func activityExportEncode(w http.ResponseWriter, logger *slog.Logger, format string, act domain.Activity, stream domain.Stream) ([]byte, string, bool) {
	switch format {
	case "gpx":
		return export.GPX(act, stream), "application/gpx+xml", true
	case "tcx":
		return export.TCX(act, stream), "application/vnd.garmin.tcx+xml", true
	case "fit":
		data, err := export.FIT(act, stream)
		if err != nil {
			logger.Error("export: fit encode failed", "activity", act.ID, "error", err)
			http.Error(w, "fit encode failed", http.StatusInternalServerError)
			return nil, "", false
		}
		return data, "application/vnd.ant.fit", true
	default:
		http.Error(w, "unsupported format", http.StatusBadRequest)
		return nil, "", false
	}
}

func activityExportFileHandler(app *App, logger *slog.Logger, format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		act, stream, ok := activityExportLoad(app, logger, w, r)
		if !ok {
			return
		}
		if len(stream.Samples) == 0 {
			http.Error(w, "this activity has no stream samples to export", http.StatusBadRequest)
			return
		}
		opts, err := export.OptionsFromExclude(r.URL.Query().Get("exclude"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		act, stream = export.Apply(opts, act, stream)

		data, contentType, ok := activityExportEncode(w, logger, format, act, stream)
		if !ok {
			return
		}

		// GPX needs at least one GPS point; if none, steer the user to TCX.
		if format == "gpx" && !strings.Contains(string(data), "<trkpt") {
			http.Error(w, "this activity has no GPS track — export as TCX instead", http.StatusBadRequest)
			return
		}

		filename := export.Filename(act, format)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data) //nolint:gosec // G705: non-HTML content type served as an attachment
	}
}
