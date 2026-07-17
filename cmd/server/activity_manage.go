package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/usecase/besteffort"
	"github.com/johnnycube/cairn-core/internal/usecase/segment"
	"github.com/johnnycube/cairn-core/internal/usecase/trainingload"
)

// fieldGroupLabels maps the merge engine's field-group keys to user-facing
// labels for the activity-manage provenance view. Unknown keys fall back to
// the raw key (forward-compatible if a new group is added before this map).
var fieldGroupLabels = map[domain.FieldGroup]string{
	domain.FieldGroupClassification: "Type & flags",
	domain.FieldGroupTime:           "Time",
	domain.FieldGroupDuration:       "Duration",
	domain.FieldGroupTitle:          "Title",
	domain.FieldGroupDescription:    "Description",
	domain.FieldGroupDistance:       "Distance",
	domain.FieldGroupElevation:      "Elevation",
	domain.FieldGroupSpeed:          "Speed",
	domain.FieldGroupHeartRate:      "Heart rate",
	domain.FieldGroupPower:          "Power",
	domain.FieldGroupCadence:        "Cadence",
	domain.FieldGroupTemperature:    "Temperature",
	domain.FieldGroupCalories:       "Calories",
	domain.FieldGroupTrainingLoad:   "Training load",
	domain.FieldGroupSwim:           "Swim",
	domain.FieldGroupGPSTrack:       "GPS track",
	domain.FieldGroupLaps:           "Laps",
}

func fieldGroupLabel(g domain.FieldGroup) string {
	if l, ok := fieldGroupLabels[g]; ok {
		return l
	}
	return string(g)
}

// mountActivityManage wires the user-facing activity "manage" surface: a
// read-only technical-insights view plus the maintenance actions that run
// fully in-process today (re-merge from sources, recompute derived data).
// Source-level detach + raw download live in activity_sources.go; together
// they back the /activities/{id}/manage page.
//
//	GET  /api/activities/{id}/manage             → provenance + per-source insights
//	POST /api/activities/{id}/recompute          → re-merge from current sources
//	POST /api/activities/{id}/recompute-derived  → best-efforts + segments + training-load
func mountActivityManage(mux *http.ServeMux, app *App, logger *slog.Logger) {
	// loadOwned resolves the activity and enforces ownership; returns false
	// (and writes the response) on any failure.
	loadOwned := func(w http.ResponseWriter, r *http.Request) (domain.Activity, bool) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return domain.Activity{}, false
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad activity id", http.StatusBadRequest)
			return domain.Activity{}, false
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(id))
		if err != nil || act.UserID != userID || act.IsDeleted() {
			http.Error(w, "not found", http.StatusNotFound)
			return domain.Activity{}, false
		}
		return act, true
	}

	mux.HandleFunc("GET /api/activities/{id}/manage", func(w http.ResponseWriter, r *http.Request) {
		act, ok := loadOwned(w, r)
		if !ok {
			return
		}

		// All sources incl. detached, for the audit view.
		sources, err := app.Activities.ListAllSourcesForActivity(r.Context(), act.ID)
		if err != nil {
			logger.Error("manage: list sources failed", "activity_id", act.ID, "error", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}

		// Reverse the merge provenance: source id → the field groups it won.
		wonBy := map[domain.SourceID][]string{}
		for g, sid := range act.MergeProvenance {
			wonBy[sid] = append(wonBy[sid], fieldGroupLabel(g))
		}

		// Per-field provenance with decided_by (rule vs manual pin) + the source's
		// provider, for the source-badge display (brief §10). decided_by is
		// derived from the field_source_overrides table rather than persisted in
		// the provenance column.
		manualFields := map[domain.FieldGroup]bool{}
		if app.FieldOverrides != nil {
			if pins, perr := app.FieldOverrides.ListForActivity(r.Context(), act.ID); perr == nil {
				for _, p := range pins {
					manualFields[p.FieldKey] = true
				}
			}
		}
		providerBySource := map[domain.SourceID]string{}
		for _, s := range sources {
			providerBySource[s.ID] = s.Provider
		}
		provenance := make([]map[string]any, 0, len(act.MergeProvenance))
		for g, sid := range act.MergeProvenance {
			decidedBy := "rule"
			if manualFields[g] {
				decidedBy = "manual"
			}
			provenance = append(provenance, map[string]any{
				"field":       string(g),
				"field_label": fieldGroupLabel(g),
				"source_id":   sid.String(),
				"provider":    providerBySource[sid],
				"decided_by":  decidedBy,
				"synced_at":   act.MergedAt.UTC(),
			})
		}
		sort.Slice(provenance, func(i, j int) bool {
			return provenance[i]["field"].(string) < provenance[j]["field"].(string)
		})

		primary := ""
		if act.PrimaryStreamSourceID != nil {
			primary = act.PrimaryStreamSourceID.String()
		}

		// Live workers, to flag sources a compatible worker can re-parse:
		// re-parse is possible only when a worker with the SAME provider +
		// version + package as the importer is online. Those three are the
		// compatibility contract; it is the maintainer's job to keep a
		// (provider, package, version) triple parse-compatible.
		workers := currentWorkers(r.Context(), app)

		out := make([]map[string]any, 0, len(sources))
		for _, s := range sources {
			var extAcct string
			if s.ExternalAccountID != nil {
				extAcct = s.ExternalAccountID.String()
			}
			var lastReimport any
			if s.LastReimportedAt != nil {
				lastReimport = s.LastReimportedAt.UTC()
			}
			// Re-parse is possible only when the source has an archived blob AND
			// a live worker matches its provider + version + package.
			reparseEligible := s.RawBlobID != "" &&
				canReparseSource(workers, s.Provider, s.SourceWorkerVersion, s.SourceWorkerPackage)
			out = append(out, map[string]any{
				"id":                 s.ID.String(),
				"provider":           s.Provider,
				"external_id":        s.ExternalID,
				"external_account":   extAcct,
				"worker_name":        s.SourceWorkerName,
				"worker_version":     s.SourceWorkerVersion,
				"worker_package":     s.SourceWorkerPackage,
				"reparse_eligible":   reparseEligible,
				"has_blob":           s.RawBlobID != "",
				"raw_content_type":   s.RawContentType,
				"raw_size_bytes":     s.RawSizeBytes,
				"has_stream":         s.Parsed.HasStream,
				"lap_count":          len(s.Parsed.Laps),
				"status":             string(s.Status),
				"status_reason":      s.StatusReason,
				"reimport_status":    string(s.ReimportStatus),
				"reimport_reason":    s.ReimportStatusReason,
				"imported_at":        s.ImportedAt.UTC(),
				"last_reimported_at": lastReimport,
				"updated_at":         s.UpdatedAt.UTC(),
				"is_primary":         s.ID.String() == primary,
				"won_field_groups":   wonBy[s.ID],
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"activity": map[string]any{
				"id":           act.ID.String(),
				"title":        act.Title,
				"type":         string(act.Type),
				"start_time":   act.StartTime.UTC(),
				"merged_at":    act.MergedAt.UTC(),
				"timezone":     act.Timezone,
				"source_count": len(sources),
				"primary":      primary,
			},
			"sources":    out,
			"provenance": provenance,
		})
	})

	mux.HandleFunc("POST /api/activities/{id}/recompute", func(w http.ResponseWriter, r *http.Request) {
		act, ok := loadOwned(w, r)
		if !ok {
			return
		}
		result, err := app.RecomputeActivity.Execute(r.Context(), act.ID)
		if err != nil {
			logger.Error("manage: recompute failed", "activity_id", act.ID, "error", err)
			http.Error(w, "recompute failed", http.StatusInternalServerError)
			return
		}
		// Last source gone → propagate the deletion to remote followers.
		if result.SoftDeleted {
			publishActivityDelete(r.Context(), app, logger, act.UserID, act.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"recomputed":   true,
			"soft_deleted": result.SoftDeleted,
			"source_count": len(result.SourceIDs),
		})
	})

	mux.HandleFunc("POST /api/activities/{id}/recompute-derived", func(w http.ResponseWriter, r *http.Request) {
		act, ok := loadOwned(w, r)
		if !ok {
			return
		}
		res := recomputeDerivedForActivity(r.Context(), app, logger, act)
		writeJSON(w, http.StatusOK, res)
	})

	logger.Info("activity manage endpoints mounted")
}

// liveWorker is the identity of a currently-online worker that matters for
// re-parse: provider + version + package. Those three ARE the compatibility
// contract — the maintainer guarantees that a given (provider, package, version)
// triple parses a blob the same way. Worker NAME/alias is deliberately NOT part
// of the identity: names are admin-assigned routing labels, not code identity.
type liveWorker struct {
	provider string
	version  string
	pkg      string
}

// currentWorkers reads the worker-presence KV and returns the identity of every
// live worker. Empty when NATS isn't wired / nothing is online — callers treat
// that as "no compatible worker, re-parse not possible".
func currentWorkers(ctx context.Context, app *App) []liveWorker {
	var out []liveWorker
	if app.NATSBus == nil {
		return out
	}
	kv, err := app.NATSBus.KV("cairn_worker_presence")
	if err != nil {
		return out
	}
	keys, _ := kv.Keys(ctx)
	now := time.Now()
	for _, k := range keys {
		entry, err := kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var hb struct {
			Provider string `json:"provider"`
			Version  string `json:"version"`
			Package  string `json:"package"`
			LastSeen string `json:"last_seen"`
		}
		if json.Unmarshal(entry.Value, &hb) != nil {
			continue
		}
		if ts, perr := time.Parse(time.RFC3339, hb.LastSeen); perr == nil && now.Sub(ts) > presenceStaleAfter {
			continue // stale ghost
		}
		out = append(out, liveWorker{provider: hb.Provider, version: hb.Version, pkg: hb.Package})
	}
	return out
}

// canReparseSource reports whether a live worker can re-parse this source's
// archived blob — i.e. a worker is online whose provider + version + package
// exactly match the importer that produced the blob. Those three are the
// maintainer-upheld compatibility contract; nothing else (notably not a build
// hash) gates eligibility.
func canReparseSource(workers []liveWorker, provider, version, pkg string) bool {
	if pkg == "" {
		return false // legacy source without package provenance — can't match
	}
	for _, w := range workers {
		if w.provider == provider && w.version == version && w.pkg == pkg {
			return true
		}
	}
	return false
}

// recomputeDerivedForActivity re-runs the per-source derived computations
// (stream aggregates, best-efforts, segment matches) for every non-detached
// source of an activity, then recomputes the user's training load once. It
// mirrors result_router.runFollowUps but is keyed off an existing activity
// rather than a fresh ingest. Each step is best-effort: a failure is recorded
// in the response but doesn't abort the rest (the same graceful-degradation
// rule the ingest pipeline follows).
func recomputeDerivedForActivity(ctx context.Context, app *App, logger *slog.Logger, act domain.Activity) map[string]any {
	warnings := []string{}
	warn := func(step string, err error) {
		logger.Warn("recompute-derived step failed", "activity_id", act.ID, "step", step, "error", err)
		warnings = append(warnings, step+": "+err.Error())
	}

	sources, err := app.Activities.ListSourcesForActivity(ctx, act.ID)
	if err != nil {
		warn("list_sources", err)
		return map[string]any{"recomputed": false, "warnings": warnings}
	}

	for _, s := range sources {
		if app.Streams != nil {
			if err := app.Streams.RefreshAggregates(ctx, s.ID); err != nil {
				warn("refresh_aggregates", err)
			}
		}
		if app.ComputeBestEfforts != nil {
			if _, err := app.ComputeBestEfforts.Execute(ctx, besteffort.Input{ActivitySourceID: s.ID}); err != nil {
				warn("best_efforts", err)
			}
		}
		if app.MatchSegments != nil {
			if _, err := app.MatchSegments.Execute(ctx, segment.Input{ActivitySourceID: s.ID}); err != nil {
				warn("match_segments", err)
			}
		}
	}

	if app.ComputeTrainingLoad != nil {
		end := time.Now().UTC()
		if _, err := app.ComputeTrainingLoad.Execute(ctx, trainingload.Input{
			UserID:     act.UserID,
			Start:      end.AddDate(0, 0, -730),
			End:        end,
			WarmUpDays: 42,
		}); err != nil {
			warn("training_load", err)
		}
	}

	return map[string]any{
		"recomputed":   true,
		"source_count": len(sources),
		"warnings":     warnings,
	}
}
