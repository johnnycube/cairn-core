package main

import (
	"log/slog"
	"math"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// activities_heatmap serves every GPS track the owner's activities have, in
// one response, for the combined "all my tracks" map.
//
//	GET /api/activities/heatmap
//	  ?<the standard activity filter — see activities_feed.go>
//	  &res=30s|5s                 stream resolution (default 30s)
//	  → { matched, with_track, truncated, facets:{...},
//	      geojson: FeatureCollection<LineString>{properties:{id,type,discipline}} }
//
// Owner-only: the viewer's own activities, so no privacy-zone trimming
// (the owner sees the full track on the activity page too). Tracks come from
// the downsampled continuous aggregates — a heatmap doesn't need raw fidelity
// and 30 s keeps years of riding to a few MB.

// heatmapMaxActivities bounds one response; beyond it the newest activities
// win and `truncated` is set so the UI can say so.
const heatmapMaxActivities = 5000

// heatmapBatch is how many sources one QueryGeoTracks round trip covers.
const heatmapBatch = 500

func mountActivitiesHeatmap(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/activities/heatmap", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		res := domain.StreamResolution30s
		if q.Get("res") == string(domain.StreamResolution5s) {
			res = domain.StreamResolution5s
		}
		filter := parseActivityFilter(q)
		filter.Sort = "date_desc"
		filter.Limit = heatmapMaxActivities

		types, disciplines, _, err := app.Activities.ActivityFacets(r.Context(), userID, filter)
		if err != nil {
			logger.Error("heatmap facets failed", "error", err)
			http.Error(w, "facets failed", http.StatusInternalServerError)
			return
		}
		acts, matched, err := app.Activities.ListActivitiesFiltered(r.Context(), userID, filter)
		if err != nil {
			logger.Error("heatmap list failed", "error", err)
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		yearStats, _ := app.Activities.ActivityYearStats(r.Context(), userID)
		years := make([]feedFacet, 0, len(yearStats))
		for _, y := range yearStats {
			years = append(years, feedFacet{Value: itoa(y.Year), Count: y.Count})
		}

		// Sources to fetch, in activity order; activities without a stream
		// (manual entries, indoor) are skipped up front.
		type item struct {
			act domain.Activity
			src domain.SourceID
		}
		items := make([]item, 0, len(acts))
		for _, a := range acts {
			if a.PrimaryStreamSourceID != nil {
				items = append(items, item{act: a, src: *a.PrimaryStreamSourceID})
			}
		}

		features := make([]map[string]any, 0, len(items))
		for start := 0; start < len(items); start += heatmapBatch {
			end := min(start+heatmapBatch, len(items))
			ids := make([]domain.SourceID, 0, end-start)
			for _, it := range items[start:end] {
				ids = append(ids, it.src)
			}
			tracks, err := app.Streams.QueryGeoTracks(r.Context(), ids, res)
			if err != nil {
				logger.Error("heatmap tracks failed", "error", err)
				http.Error(w, "tracks failed", http.StatusInternalServerError)
				return
			}
			for _, it := range items[start:end] {
				pts := tracks[it.src]
				if len(pts) < 2 {
					continue
				}
				coords := make([][2]float64, len(pts))
				for i, p := range pts {
					// ~1 m precision is plenty for a heatmap and trims the payload.
					coords[i] = [2]float64{round5(p.Lon), round5(p.Lat)}
				}
				features = append(features, map[string]any{
					"type": "Feature",
					"id":   it.act.ID.String(),
					"properties": map[string]any{
						"id":         it.act.ID.String(),
						"type":       string(it.act.Type),
						"discipline": string(it.act.Discipline),
					},
					"geometry": map[string]any{"type": "LineString", "coordinates": coords},
				})
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"matched":    matched,
			"with_track": len(features),
			"truncated":  matched > len(acts),
			"facets": map[string]any{
				"types":       toFeedFacets(types),
				"disciplines": toFeedFacets(disciplines),
				"years":       years,
			},
			"geojson": map[string]any{"type": "FeatureCollection", "features": features},
		})
	})

	logger.Info("activities heatmap endpoint mounted", "path", "/api/activities/heatmap")
}

func round5(v float64) float64 { return math.Round(v*1e5) / 1e5 }
