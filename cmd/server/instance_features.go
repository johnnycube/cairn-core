package main

import (
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/config"
)

// instance_features tells the SPA which optional, instance-level features are
// switched on so it can hide the UI for dormant ones. Public and unauthenticated
// on purpose: it exposes only booleans an anonymous visitor could infer anyway
// (e.g. federation answers /.well-known/webfinger when on).
//
//	GET /api/instance/features → { "federation": bool }
func mountInstanceFeatures(mux *http.ServeMux, cfg *config.Config, logger *slog.Logger) {
	mux.HandleFunc("GET /api/instance/features", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, http.StatusOK, map[string]any{
			"federation": cfg.Federation.Enabled,
		})
	})
	logger.Info("instance features endpoint mounted", "federation", cfg.Federation.Enabled)
}
