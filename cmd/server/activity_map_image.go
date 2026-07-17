package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/staticmap"
)

// activity_map_image.go serves a static PNG snapshot of an activity's course —
// the GPS polyline over a basemap — for feeds, list thumbnails, and federation
// (a stable image URL, not an interactive map).
//
// Visibility: the owner gets the full track; a non-owner gets it only when the
// resolved CategorySet grants `map`, and then the track is privacy-zone-trimmed
// (segments inside the owner's zones are dropped) — the same masking the rest of
// the projection applies. Rendered images are cached in S3 by (activity, variant).

const mapImageCacheControl = "public, max-age=86400"

// mapImageStyleVersion namespaces the S3 cache. Bump it whenever the rendered
// style changes (colours, markers, tile density) so stale snapshots are bypassed
// instead of served. v2: orange casing + start/finish markers + retina tiles.
// v3: 2:1 landscape framing to fit the feed thumbnail boxes without cropping.
// v4: 3:1 short banner so the snapshot takes less vertical space in feeds.
const mapImageStyleVersion = "v4"

// cartoTileFetcher pulls key-less CARTO Voyager raster tiles (the same basemap
// the interactive map uses). Render-time only; results are cached in S3 so this
// runs once per activity+variant, not per request.
var cartoHTTPClient = &http.Client{Timeout: 15 * time.Second}

func cartoTileFetcher(ctx context.Context, z, x, y int) (image.Image, error) {
	sub := []string{"a", "b", "c"}[(x+y)%3]
	// @2x retina tiles (512 px) — the exact basemap the interactive MapLibre
	// view uses, so the snapshot matches it instead of looking coarse.
	url := fmt.Sprintf("https://%s.basemaps.cartocdn.com/rastertiles/voyager/%d/%d/%d@2x.png", sub, z, x, y)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Cairn/1 (static map)")
	resp, err := cartoHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tile %d/%d/%d: status %d", z, x, y, resp.StatusCode)
	}
	return png.Decode(resp.Body)
}

func mountActivityMapImage(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/activities/{id}/map.png", func(w http.ResponseWriter, r *http.Request) {
		actID, err := domain.ParseUUID[domain.ActivityID](r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), actID)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Resolve viewer + the map-visibility decision.
		var viewerPtr *domain.UserID
		if v, ok := optionalSessionUser(r, app); ok {
			viewerPtr = &v
		}
		isOwner := viewerPtr != nil && *viewerPtr == act.UserID
		trim := false
		if !isOwner {
			if act.Privacy == domain.PrivacyPrivate || act.HiddenByAdmin {
				http.NotFound(w, r)
				return
			}
			cats, _ := visibilityFor(r.Context(), app, act.UserID, viewerPtr, act.ID, false)
			if !cats.Has(domain.CategoryMap) {
				http.NotFound(w, r) // the map isn't shared with this viewer
				return
			}
			trim = true // non-owners get the privacy-zone-trimmed track
		}

		variant := "full"
		if trim {
			variant = "trimmed"
		}
		cacheKey := "mapimg/" + mapImageStyleVersion + "/" + actID.String() + "/" + variant + ".png"

		// Serve from the S3 cache when present.
		if app.BlobStore != nil {
			if data, _, err := app.BlobStore.Get(r.Context(), cacheKey); err == nil && len(data) > 0 {
				serveMapPNG(w, data)
				return
			}
		}

		imgBytes, ok := renderActivityMap(r.Context(), app, act, trim, logger)
		if !ok {
			http.NotFound(w, r) // no renderable GPS track
			return
		}
		if app.BlobStore != nil {
			if err := app.BlobStore.Put(r.Context(), cacheKey, imgBytes, "image/png"); err != nil {
				logger.Warn("map image: cache store failed", "activity", actID, "error", err)
			}
		}
		serveMapPNG(w, imgBytes)
	})

	logger.Info("activity map-image endpoint mounted", "path", "/api/activities/{id}/map.png")
}

// renderActivityMap reads the activity's GPS track, optionally trims it against
// the owner's privacy zones, and renders the static PNG. ok=false when there is
// no renderable track (no GPS, or every point fell inside a zone).
func renderActivityMap(ctx context.Context, app *App, act domain.Activity, trim bool, logger *slog.Logger) ([]byte, bool) {
	if act.PrimaryStreamSourceID == nil {
		return nil, false
	}
	stream, err := app.Streams.QueryStream(ctx, domain.StreamQuery{
		ActivitySourceID: *act.PrimaryStreamSourceID,
		Channels:         []domain.StreamChannel{domain.StreamChannelLatitude, domain.StreamChannelLongitude},
		Resolution:       domain.StreamResolutionRaw,
	})
	if err != nil {
		logger.Warn("map image: stream query failed", "activity", act.ID, "error", err)
		return nil, false
	}

	var zones []domain.PrivacyZone
	if trim && app.Visibility != nil {
		zones, _ = app.Visibility.ListPrivacyZones(ctx, act.UserID)
	}

	pts := make([]staticmap.LatLng, 0, len(stream.Samples))
	for _, s := range stream.Samples {
		if s.Latitude == nil || s.Longitude == nil {
			continue
		}
		if len(zones) > 0 && domain.PointInAnyZone(zones, *s.Latitude, *s.Longitude) {
			continue // drop track segments inside a privacy zone
		}
		pts = append(pts, staticmap.LatLng{Lat: *s.Latitude, Lng: *s.Longitude})
	}
	pts = downsample(pts, 600)
	if len(pts) < 2 {
		return nil, false
	}

	var buf bytes.Buffer
	if err := staticmap.RenderPNG(ctx, &buf, pts, staticmap.DefaultOptions(), cartoTileFetcher); err != nil {
		logger.Warn("map image: render failed", "activity", act.ID, "error", err)
		return nil, false
	}
	return buf.Bytes(), true
}

// downsample reduces a dense track to at most max points by even stride,
// always keeping the first and last.
func downsample(pts []staticmap.LatLng, max int) []staticmap.LatLng {
	if len(pts) <= max || max < 2 {
		return pts
	}
	stride := float64(len(pts)-1) / float64(max-1)
	out := make([]staticmap.LatLng, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, pts[int(float64(i)*stride)])
	}
	return out
}

func serveMapPNG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", mapImageCacheControl)
	_, _ = w.Write(data)
}
