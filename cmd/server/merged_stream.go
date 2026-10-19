package main

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/domain/streamalign"
)

// mountMergedStream serves the per-channel-merged, aligned, resampled stream:
// best source per channel across the activity's sources, on a common grid.
// Computed on demand from the raw streams (not persisted).
//
//	GET /api/activities/{id}/merged-stream
func mountMergedStream(mux *http.ServeMux, app *App) {
	if app.Streams == nil {
		return
	}
	mux.HandleFunc("GET /api/activities/{id}/merged-stream", mergedStreamHandler(app))
}

// mergedStreamHandler serves GET /api/activities/{id}/merged-stream.
func mergedStreamHandler(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		streams := mergedStreamLoad(r, app, sources)
		if len(streams) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"grid": []int64{}, "channels": map[string]any{}})
			return
		}

		merged := streamalign.Build(streams, nil, streamalign.Options{})
		writeJSON(w, http.StatusOK, mergedStreamJSON(merged))
	}
}

// mergedStreamLoad fetches the 5s stream of every source that has one.
func mergedStreamLoad(r *http.Request, app *App, sources []domain.ActivitySource) map[domain.SourceID]domain.Stream {
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
	return streams
}

// mergedStreamJSON builds the response body of a merged stream.
func mergedStreamJSON(merged streamalign.MergedStream) map[string]any {
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
	return map[string]any{
		"grid":       grid,
		"channels":   channels,
		"provenance": provenance,
	}
}
