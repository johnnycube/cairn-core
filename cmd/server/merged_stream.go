package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/domain/streamalign"
)

// mountMergedStream serves the per-channel-merged, aligned, resampled stream:
// best source per channel across the activity's sources, on a common grid.
// Computed on demand from the raw streams (not persisted).
//
//	GET /api/activities/{id}/merged-stream
func mountMergedStream(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Streams == nil {
		return
	}
	mux.HandleFunc("GET /api/activities/{id}/merged-stream", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		aUUID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(aUUID))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		sources, err := app.Activities.ListSourcesForActivity(r.Context(), act.ID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}

		streams := map[domain.SourceID]domain.Stream{}
		for _, s := range sources {
			if !s.Parsed.HasStream {
				continue
			}
			st, err := app.Streams.QueryStream(r.Context(), domain.StreamQuery{
				ActivitySourceID: s.ID,
				Resolution:       domain.StreamResolution5s,
			})
			if err != nil || len(st.Samples) == 0 {
				continue
			}
			streams[s.ID] = st
		}
		if len(streams) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"grid": []int64{}, "channels": map[string]any{}})
			return
		}

		merged := streamalign.Build(streams, nil, streamalign.Options{})

		grid := make([]int64, len(merged.Grid))
		for i, t := range merged.Grid {
			grid[i] = t.Unix()
		}
		channels := map[string][]*float64{}
		provenance := map[string]string{}
		for ch, vals := range merged.Channels {
			channels[string(ch)] = vals
		}
		for ch, sid := range merged.Provenance {
			provenance[string(ch)] = sid.String()
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"grid":       grid,
			"channels":   channels,
			"provenance": provenance,
		})
	})
}
