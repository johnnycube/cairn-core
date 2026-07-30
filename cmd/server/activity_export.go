package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/export"
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
	// loadForExport resolves auth + activity + raw merged stream; on failure it
	// has already written the HTTP error.
	loadForExport := func(w http.ResponseWriter, r *http.Request) (domain.Activity, domain.Stream, bool) {
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

	handler := func(format string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			act, stream, ok := loadForExport(w, r)
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

			var (
				data        []byte
				contentType string
			)
			switch format {
			case "gpx":
				data = export.GPX(act, stream)
				contentType = "application/gpx+xml"
			case "tcx":
				data = export.TCX(act, stream)
				contentType = "application/vnd.garmin.tcx+xml"
			case "fit":
				data, err = export.FIT(act, stream)
				if err != nil {
					logger.Error("export: fit encode failed", "activity", act.ID, "error", err)
					http.Error(w, "fit encode failed", http.StatusInternalServerError)
					return
				}
				contentType = "application/vnd.ant.fit"
			default:
				http.Error(w, "unsupported format", http.StatusBadRequest)
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
			_, _ = w.Write(data)
		}
	}

	mux.HandleFunc("GET /api/activities/{id}/export.gpx", handler("gpx"))
	mux.HandleFunc("GET /api/activities/{id}/export.tcx", handler("tcx"))
	mux.HandleFunc("GET /api/activities/{id}/export.fit", handler("fit"))

	mux.HandleFunc("GET /api/activities/{id}/export/options", func(w http.ResponseWriter, r *http.Request) {
		act, stream, ok := loadForExport(w, r)
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
