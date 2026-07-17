// Package match (use-case) orchestrates re-clustering: pull a bucket of source
// records, run the pure matcher + reconciler, apply the plan (reassign sources,
// re-merge affected activities, dissolve emptied ones, record redirects).
package match

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	dmatch "github.com/johnnycube/cairn-core/internal/domain/match"
	"github.com/johnnycube/cairn-core/internal/domain/reconcile"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/activity"
)

// DefaultBucketMargin is the time-window half-width around an ingested activity.
// ±24h covers the UTC-day boundary and cross-timezone duplicates.
const DefaultBucketMargin = 24 * time.Hour

// ReclusterBucket re-derives the logical-activity grouping for one time bucket.
type ReclusterBucket struct {
	activities port.ActivityRepo
	recompute  *activity.RecomputeActivityFromSources
	denylist   port.SourceDenylistRepo // optional; nil → no detach denylist (Gap 6)
	tx         port.TxManager
	newID      func() uuid.UUID
	opts       dmatch.Options
	logger     *slog.Logger
}

// NewReclusterBucket wires the use-case. newID/logger default sensibly; opts
// falls back to dmatch.DefaultOptions when zero. denylist may be nil.
func NewReclusterBucket(
	activities port.ActivityRepo,
	recompute *activity.RecomputeActivityFromSources,
	denylist port.SourceDenylistRepo,
	tx port.TxManager,
	newID func() uuid.UUID,
	opts dmatch.Options,
	logger *slog.Logger,
) *ReclusterBucket {
	if newID == nil {
		newID = func() uuid.UUID {
			id, err := uuid.NewV7()
			if err != nil {
				return uuid.New()
			}
			return id
		}
	}
	if opts.EdgeThreshold == 0 && opts.HighBand == 0 {
		opts = dmatch.DefaultOptions()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ReclusterBucket{activities, recompute, denylist, tx, newID, opts, logger}
}

// Input selects the bucket: all of a user's source records within ±Margin of
// Around (defaults to DefaultBucketMargin).
type Input struct {
	UserID domain.UserID
	Around time.Time
	Margin time.Duration
}

// Result summarizes what the re-cluster did.
type Result struct {
	AffectedActivityIDs []domain.ActivityID
	SoftDeleted         []domain.ActivityID
	Conflicts           int
}

// Execute re-clusters the bucket transactionally.
func (uc *ReclusterBucket) Execute(ctx context.Context, in Input) (Result, error) {
	margin := in.Margin
	if margin <= 0 {
		margin = DefaultBucketMargin
	}
	from := in.Around.Add(-margin)
	to := in.Around.Add(margin)

	var result Result
	err := uc.tx.InTx(ctx, func(ctx context.Context) error {
		records, err := uc.activities.ListSourceRecordsInBucket(ctx, in.UserID, from, to)
		if err != nil {
			return fmt.Errorf("list bucket: %w", err)
		}
		if len(records) <= 1 {
			return nil // nothing to recluster against
		}

		mrecs := make([]dmatch.Record, 0, len(records))
		existing := make(map[string]string, len(records))
		inBucket := make(map[string]bool, len(records))
		var deniedSingletons []string // denied sources kept standalone (Gap 6)
		for _, r := range records {
			sid := r.SourceID.String()
			inBucket[sid] = true
			existing[sid] = r.ActivityID.String()

			// Gap-6: a previously-detached identity must not auto-merge on
			// re-push — keep it standalone (the denylist survives the new source id).
			if uc.denylist != nil {
				denied, derr := uc.denylist.IsDenied(ctx, in.UserID, r.Provider, r.ExternalAccountID, r.ExternalID)
				if derr != nil {
					return fmt.Errorf("denylist check: %w", derr)
				}
				if denied {
					deniedSingletons = append(deniedSingletons, sid)
					continue
				}
			}

			mrecs = append(mrecs, dmatch.Record{
				ID: sid,
				SourceFeatures: dmatch.SourceFeatures{
					Provider:   r.Provider,
					SportClass: r.SportClass,
					StartUTC:   r.StartUTC,
					DistanceM:  derefF(r.DistanceM),
					MovingS:    r.MovingS,
					ElapsedS:   r.ElapsedS,
					StartLat:   r.StartLat,
					StartLng:   r.StartLng,
				},
			})
		}

		// Manual constraints scoped to this bucket.
		cons, err := uc.activities.ListMatchConstraints(ctx, in.UserID)
		if err != nil {
			return fmt.Errorf("list constraints: %w", err)
		}
		var mc dmatch.Constraints
		for _, c := range cons {
			a, b := c.SourceA.String(), c.SourceB.String()
			if !inBucket[a] || !inBucket[b] {
				continue
			}
			switch c.Kind {
			case domain.ConstraintMustLink:
				mc.MustLink = append(mc.MustLink, [2]string{a, b})
			case domain.ConstraintCannotLink:
				mc.CannotLink = append(mc.CannotLink, [2]string{a, b})
			}
		}

		clustered := dmatch.ClusterBucket(mrecs, mc, uc.opts)
		result.Conflicts = len(clustered.Conflicts)
		for _, cf := range clustered.Conflicts {
			uc.logger.Warn("recluster: same-provider conflict (not merged)",
				"provider", cf.Provider, "a", cf.AID, "b", cf.BID, "score", cf.Score)
		}

		// Reconcile clusters → stable activity ids. Denied sources are appended
		// as their own singleton clusters so they stay standalone.
		bandByKey := map[string]dmatch.Band{}
		rcl := make([]reconcile.ClusterInput, 0, len(clustered.Clusters)+len(deniedSingletons))
		for _, cl := range clustered.Clusters {
			rcl = append(rcl, reconcile.ClusterInput{SourceIDs: cl.RecordIDs})
			bandByKey[clusterKey(cl.RecordIDs)] = cl.Confidence
		}
		for _, sid := range deniedSingletons {
			rcl = append(rcl, reconcile.ClusterInput{SourceIDs: []string{sid}})
			bandByKey[clusterKey([]string{sid})] = dmatch.BandHigh
		}
		plan := reconcile.Reconcile(rcl, existing, func() string { return uc.newID().String() })

		// Reassign sources whose activity changed.
		bandByActivity := map[string]dmatch.Band{}
		for _, d := range plan.Decisions {
			bandByActivity[d.ActivityID] = bandByKey[clusterKey(d.SourceIDs)]
			newAct, err := parseActivityID(d.ActivityID)
			if err != nil {
				return err
			}
			for _, sid := range d.SourceIDs {
				if existing[sid] == d.ActivityID {
					continue
				}
				srcID, err := parseSourceID(sid)
				if err != nil {
					return err
				}
				if err := uc.activities.ReassignSource(ctx, srcID, newAct); err != nil {
					return err
				}
			}
		}

		// Affected = every decision target ∪ every old activity in the bucket.
		affected := map[string]bool{}
		for _, d := range plan.Decisions {
			affected[d.ActivityID] = true
		}
		for _, old := range existing {
			affected[old] = true
		}
		ids := make([]string, 0, len(affected))
		for a := range affected {
			ids = append(ids, a)
		}
		sort.Strings(ids)

		for _, a := range ids {
			aid, err := parseActivityID(a)
			if err != nil {
				return err
			}
			rec, err := uc.recompute.Execute(ctx, aid)
			if err != nil {
				return fmt.Errorf("recompute %s: %w", a, err)
			}
			if rec.SoftDeleted {
				result.SoftDeleted = append(result.SoftDeleted, aid)
				continue
			}
			if band, ok := bandByActivity[a]; ok {
				if err := uc.activities.SetActivityMatchState(ctx, aid, string(band), band == dmatch.BandMedium); err != nil {
					return err
				}
				result.AffectedActivityIDs = append(result.AffectedActivityIDs, aid)
			}
		}

		// Record merge redirects so old ids still resolve.
		for _, rd := range plan.Redirects {
			oldID, err := parseActivityID(rd[0])
			if err != nil {
				return err
			}
			newID, err := parseActivityID(rd[1])
			if err != nil {
				return err
			}
			if err := uc.activities.AddActivityRedirect(ctx, oldID, newID, "merge"); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func clusterKey(ids []string) string { return strings.Join(ids, ",") }

func derefF(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func parseActivityID(s string) (domain.ActivityID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return domain.ActivityID{}, fmt.Errorf("parse activity id %q: %w", s, err)
	}
	return domain.ActivityID(u), nil
}

func parseSourceID(s string) (domain.SourceID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return domain.SourceID{}, fmt.Errorf("parse source id %q: %w", s, err)
	}
	return domain.SourceID(u), nil
}
