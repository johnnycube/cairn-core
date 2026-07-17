package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/johnnycube/cairn-core/internal/port"
)

// startManifestWatcher launches a goroutine watching the
// cairn_worker_manifests KV bucket. Every time a worker writes a fresh
// manifest (typically on startup; possibly on hash-change during a
// rolling deploy), the watcher fires the mark-out-of-date pipeline.
//
// Concretely: when a worker publishes a manifest with a HIGHER version than
// the version that imported a source (same provider + package), every such
// activity_source is flipped to reimport_status='update_available'. The
// next reconcile / admin-trigger then enqueues parse_blob.<provider>
// jobs to bring those sources up to date.
//
// Gated on app.NATSBus; returns (nil, nil) when NATS isn't wired.
//
// The watcher is intentionally fail-soft: any error in a single
// update is logged and the loop continues. The KV-watch reconnects
// transparently across NATS reconnects.
func startManifestWatcher(
	ctx context.Context,
	app *App,
	logger *slog.Logger,
) (port.KVWatcher, error) {
	if app.NATSBus == nil {
		return nil, nil
	}
	log := logger.With("component", "manifest_watcher")

	kv, err := app.NATSBus.KV("cairn_worker_manifests")
	if err != nil {
		return nil, fmt.Errorf("kv worker_manifests: %w", err)
	}

	// "*" matches every key in the bucket (each key is a worker_name).
	// The watch starts from "current state" — every active worker's
	// manifest immediately replays, which is what we want at boot
	// (catch any drift that accumulated while the server was down).
	watcher, err := kv.Watch(ctx, "*")
	if err != nil {
		return nil, fmt.Errorf("watch worker_manifests: %w", err)
	}

	go func() {
		log.Info("manifest watcher started")
		for {
			select {
			case <-ctx.Done():
				log.Info("manifest watcher shutting down")
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					return
				}
				if len(entry.Value) == 0 {
					continue // tombstone (worker offboarded) — nothing to do
				}
				handleManifestUpdate(ctx, app, log, entry)
			}
		}
	}()

	return watcher, nil
}

func handleManifestUpdate(ctx context.Context, app *App, log *slog.Logger, entry port.KVEntry) {
	var m manifestPayloadWire
	if err := json.Unmarshal(entry.Value, &m); err != nil {
		log.Warn("decode manifest payload failed", "key", entry.Key, "error", err)
		return
	}
	// The update-available trigger keys on (provider, package, version) — the
	// routing name/alias is irrelevant. Legacy manifests without provider/
	// package can't drive it; skip them (their sources stay 'current').
	if m.Provider == "" || m.Package == "" {
		return
	}
	version, err := strconv.Atoi(strings.TrimSpace(m.Version))
	if err != nil {
		log.Warn("manifest version not an integer; cannot drive update-available",
			"worker_name", m.WorkerName, "version", m.Version)
		return
	}

	// Pure-SQL: flag every 'current' source from the same package+provider with
	// a lower version. Idempotent — re-running for the same version is a no-op.
	updated, err := app.Activities.MarkSourcesOutOfDate(ctx, m.Provider, m.Package, version)
	if err != nil {
		log.Warn("mark sources out-of-date failed",
			"provider", m.Provider, "package", m.Package, "version", version, "error", err)
		return
	}
	if updated > 0 {
		log.Info("flagged sources for reimport (newer worker version)",
			"provider", m.Provider, "package", m.Package, "version", version, "count", updated)
	}
}

// manifestPayloadWire is the JSON shape workers write to the
// cairn_worker_manifests KV bucket. Keep in sync with
// internal/workersdk/worker.go:manifestPayload.
type manifestPayloadWire struct {
	WorkerName string `json:"worker_name"`
	Version    string `json:"version"`
	Provider   string `json:"provider"`
	Package    string `json:"package"`
	UpdatedAt  string `json:"updated_at"`
}
