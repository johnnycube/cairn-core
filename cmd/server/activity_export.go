package main

import (
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
//	GET /api/activities/{id}/export.gpx
//	GET /api/activities/{id}/export.tcx
func mountActivityExport(mux *http.ServeMux, app *App, logger *slog.Logger) {
	handler := func(format string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			userID, ok := resolveSessionUser(r, app)
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			id, err := uuid.Parse(r.PathValue("id"))
			if err != nil {
				http.Error(w, "bad activity id", http.StatusBadRequest)
				return
			}
			act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(id))
			if err != nil || act.UserID != userID || act.IsDeleted() {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if act.PrimaryStreamSourceID == nil {
				http.Error(w, "this activity has no stream to export", http.StatusBadRequest)
				return
			}

			stream, err := app.Streams.QueryStream(r.Context(), domain.StreamQuery{
				ActivitySourceID: *act.PrimaryStreamSourceID,
				Channels:         nil, // all channels
				Resolution:       domain.StreamResolutionRaw,
			})
			if err != nil {
				logger.Error("export: stream query failed", "activity", act.ID, "error", err)
				http.Error(w, "stream query failed", http.StatusInternalServerError)
				return
			}
			if len(stream.Samples) == 0 {
				http.Error(w, "this activity has no stream samples to export", http.StatusBadRequest)
				return
			}

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

	logger.Info("activity export endpoints mounted")
}
