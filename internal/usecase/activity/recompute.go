// Package activity contains use cases that operate on the Activity
// aggregate. The package depends only on internal/domain (types) and
// internal/port (driven interfaces) — no DB, HTTP, or proto imports.
package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// RecomputeActivityFromSources is the deterministic re-merge use case.
// It is invoked whenever the set of ActivitySources for an Activity
// changes (new import, reimport, detach, payload preview commit) and
// is safe to re-run idempotently — same source set + same UserSettings
// ⇒ same Activity row.
//
// The use case takes a transaction snapshot of the activity and its
// sources, runs the pure-function merge engine, applies the user-edit
// preservation rules, validates, and persists.
type RecomputeActivityFromSources struct {
	activities    port.ActivityRepo
	settings      port.UserSettingsRepo
	athlete       port.AthleteProfileRepo            // optional; nil → TSS uses engine defaults
	overrides     port.FieldOverrideRepo             // optional; nil → no manual field pins
	classOverride port.ClassificationOverrideRepo    // optional; nil → no user classification overlay
	tx            port.TxManager
	now           func() time.Time
}

// NewRecomputeActivityFromSources wires the dependencies. now defaults
// to time.Now when nil — injection is for deterministic tests. athlete may be
// nil (TSS then falls back to engine-default thresholds).
func NewRecomputeActivityFromSources(
	activities port.ActivityRepo,
	settings port.UserSettingsRepo,
	athlete port.AthleteProfileRepo,
	overrides port.FieldOverrideRepo,
	classOverride port.ClassificationOverrideRepo,
	tx port.TxManager,
	now func() time.Time,
) *RecomputeActivityFromSources {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &RecomputeActivityFromSources{
		activities:    activities,
		settings:      settings,
		athlete:       athlete,
		overrides:     overrides,
		classOverride: classOverride,
		tx:            tx,
		now:           now,
	}
}

// RecomputeResult summarises what changed. Callers (typically the
// ingest pipeline) use it to decide whether to dispatch notifications
// and to re-run downstream computations (best efforts, segment matching).
type RecomputeResult struct {
	// Activity is the merged result. Zero-valued when SoftDeleted is true.
	Activity domain.Activity

	// SourceIDs lists every non-detached source contributing to Activity.
	SourceIDs []domain.SourceID

	// Created is true when this was the first merge for the activity
	// (no Activity row existed prior).
	Created bool

	// SoftDeleted is true when the activity had no remaining sources
	// and was soft-deleted instead of re-merged.
	SoftDeleted bool
}

// Execute runs the use case. The activityID must already exist as a row
// in either activities or activity_sources — the caller is responsible
// for creating the activities skeleton (Create+SaveActivity) when ingesting
// a brand new activity before invoking Execute.
//
// Steps:
//
//  1. Read the previous Activity (if any) and its sources atomically.
//  2. If zero sources: soft-delete the activity and return.
//  3. Otherwise: build a PolicyResolver bound to the user, call the
//     pure-function merge engine, then preserve user-controlled fields
//     (Title/Description/Tags/Privacy/GearID/CreatedAt) from the prior row.
//  4. Validate and persist.
func (uc *RecomputeActivityFromSources) Execute(
	ctx context.Context,
	id domain.ActivityID,
) (RecomputeResult, error) {
	var result RecomputeResult

	err := uc.tx.InTx(ctx, func(ctx context.Context) error {
		// Step 1: snapshot the previous state.
		prev, err := uc.activities.GetActivity(ctx, id)
		prevExists := err == nil
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("get prior activity: %w", err)
		}

		sources, err := uc.activities.ListSourcesForActivity(ctx, id)
		if err != nil {
			return fmt.Errorf("list sources: %w", err)
		}

		// Step 2: zero sources — soft-delete the activity.
		if len(sources) == 0 {
			if !prevExists {
				// Nothing to delete; treat as a no-op success. Common
				// during failed initial ingest cleanup.
				return nil
			}
			if err := uc.activities.SoftDeleteActivity(ctx, id, uc.now()); err != nil {
				return fmt.Errorf("soft delete: %w", err)
			}
			result.SoftDeleted = true
			return nil
		}

		// Step 3: merge, applying any manual per-field source pins (re-read each
		// run so they survive recompute).
		userID := sources[0].UserID
		resolver := uc.buildResolver(ctx, userID)

		var overrides map[domain.FieldGroup]domain.SourceID
		if uc.overrides != nil {
			pins, err := uc.overrides.ListForActivity(ctx, id)
			if err != nil {
				return fmt.Errorf("load field overrides: %w", err)
			}
			if len(pins) > 0 {
				overrides = make(map[domain.FieldGroup]domain.SourceID, len(pins))
				for _, p := range pins {
					overrides[p.FieldKey] = p.SourceID
				}
			}
		}

		merged, err := domain.MergeWithOverrides(id, sources, resolver, overrides, uc.now())
		if err != nil {
			return fmt.Errorf("merge: %w", err)
		}

		if prevExists {
			preserveUserEdits(&merged, prev)
		}

		// Apply the user's classification overlay (type/discipline/flags) on top
		// of the merge, so a manual correction survives re-derivation. Loaded
		// each run; an empty/absent overlay is a no-op.
		if uc.classOverride != nil {
			co, err := uc.classOverride.Get(ctx, id)
			if err != nil {
				return fmt.Errorf("load classification override: %w", err)
			}
			co.ApplyTo(&merged)
			// An override can change Type independently of Discipline; drop a
			// now-incompatible discipline so the result stays valid.
			if !merged.Discipline.CompatibleWith(merged.Type) {
				merged.Discipline = domain.DisciplineNone
			}
		}

		// Derive a training-stress score when no source provided one, so the
		// training-load curves have per-activity TSS to integrate. A provider-
		// supplied TSS always wins; this only fills the (common) gap. Thresholds
		// (FTP / LTHR) are resolved from the athlete's profile AS OF the
		// activity's date, so re-computing an old activity uses the values that
		// were true back then.
		if merged.Summary.TSS == nil {
			th := uc.thresholdsAt(ctx, merged.UserID, merged.StartTime)
			if tss, if_, ok := domain.EstimateTSS(merged.Summary, merged.MovingDuration, th); ok {
				merged.Summary.TSS = &tss
				if merged.Summary.IntensityFactor == nil {
					merged.Summary.IntensityFactor = &if_
				}
			}
		}

		if err := merged.Validate(); err != nil {
			return fmt.Errorf("merge produced invalid activity: %w", err)
		}

		// Step 4: persist.
		if err := uc.activities.SaveActivity(ctx, merged); err != nil {
			return fmt.Errorf("save activity: %w", err)
		}

		result.Activity = merged
		result.SourceIDs = collectSourceIDs(sources)
		result.Created = !prevExists
		return nil
	})

	return result, err
}

// thresholdsAt resolves the athlete's FTP / threshold-HR as of the given date.
// Returns the zero value (→ engine defaults) when no profile repo is wired or
// the lookup fails — TSS estimation must always make forward progress.
func (uc *RecomputeActivityFromSources) thresholdsAt(
	ctx context.Context,
	userID domain.UserID,
	at time.Time,
) domain.AthleteThresholds {
	if uc.athlete == nil {
		return domain.AthleteThresholds{}
	}
	entries, err := uc.athlete.ListEntries(ctx, userID)
	if err != nil || len(entries) == 0 {
		return domain.AthleteThresholds{}
	}
	return domain.NewAthleteProfile(entries).ThresholdsAt(at)
}

// buildResolver returns a PolicyResolver that reads from the user's
// configured per-activity-type policy and falls back to the built-in
// defaults if storage is unreachable. The closure captures ctx because
// the resolver is called synchronously from inside the merge engine.
//
// Lookup errors are logged-at-the-adapter-level and swallowed here —
// the merge engine must always make forward progress with SOME policy,
// even if the settings table is temporarily unreadable. The fallback
// is deterministic (same as a brand-new user) so re-running the merge
// once settings storage recovers will produce the canonical result.
func (uc *RecomputeActivityFromSources) buildResolver(
	ctx context.Context,
	userID domain.UserID,
) domain.PolicyResolver {
	return func(t domain.ActivityType) domain.MergePolicy {
		policy, err := uc.settings.GetMergePolicy(ctx, userID, t)
		if err != nil {
			return domain.DefaultMergePolicyFor(t)
		}
		return policy
	}
}

// preserveUserEdits copies the user-controlled fields from a previously-
// persisted Activity onto a freshly-merged one. Once an Activity has been
// created, these fields are owned by the user — subsequent re-merges
// must NOT overwrite them with values from new source imports.
//
// User-controlled fields:
//
//	Title         — user typically renames default provider titles
//	Description   — user-authored notes
//	Tags          — user-assigned labels
//	Privacy       — user-controlled visibility
//	GearID        — user-attached gear
//
// Provenance preservation:
//
//	CreatedAt     — set by the initial insert; never re-written
func preserveUserEdits(merged *domain.Activity, prev domain.Activity) {
	merged.Title = prev.Title
	merged.Description = prev.Description
	merged.Tags = prev.Tags
	merged.Privacy = prev.Privacy
	merged.GearID = prev.GearID
	merged.CreatedAt = prev.CreatedAt
}

func collectSourceIDs(sources []domain.ActivitySource) []domain.SourceID {
	ids := make([]domain.SourceID, len(sources))
	for i, s := range sources {
		ids[i] = s.ID
	}
	return ids
}
