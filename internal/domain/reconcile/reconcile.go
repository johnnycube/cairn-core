// Package reconcile maps fresh clusters onto stable logical-activity IDs: each
// cluster inherits the existing ID it shares the most sources with; splits keep
// the ID on the largest piece (fresh IDs for the rest); merges keep one ID and
// redirect + dissolve the others. Pure and deterministic.
package reconcile

import "sort"

// ClusterInput is one matcher cluster (its source-record IDs).
type ClusterInput struct {
	SourceIDs []string
}

// Decision is the resolved identity for one cluster.
type Decision struct {
	SourceIDs     []string
	ActivityID    string
	Inherited     bool
	InheritedFrom string // == ActivityID when Inherited
}

// Plan is the reconciliation output.
type Plan struct {
	Decisions  []Decision
	SoftDelete []string    // dissolved activity ids (no cluster inherited them)
	Redirects  [][2]string // (oldID, newID) for dissolved ids
}

// Reconcile computes the identity plan. existing maps current source-id →
// activity-id (absent = brand new); newID mints a fresh activity id.
func Reconcile(clusters []ClusterInput, existing map[string]string, newID func() string) Plan {
	cls := reconcileSortClusters(clusters)
	overlaps, allOld := reconcileOverlaps(cls, existing)
	bestOld, bestCnt := reconcileBestOld(overlaps)
	claimedBy := reconcileClaims(bestOld, bestCnt)

	// Assign ids: inheritors keep their claimed old id; everyone else gets fresh.
	n := len(cls)
	assigned := make([]string, n)
	inherited := make([]bool, n)
	for ci := range cls {
		old := bestOld[ci]
		if old != "" && claimedBy[old] == ci {
			assigned[ci], inherited[ci] = old, true
		} else {
			assigned[ci] = newID()
		}
	}

	softDelete, redirects := reconcileDissolved(overlaps, allOld, claimedBy, assigned)

	decisions := make([]Decision, n)
	for ci := range cls {
		d := Decision{
			SourceIDs:  cls[ci].SourceIDs,
			ActivityID: assigned[ci],
			Inherited:  inherited[ci],
		}
		if inherited[ci] {
			d.InheritedFrom = assigned[ci]
		}
		decisions[ci] = d
	}

	return Plan{Decisions: decisions, SoftDelete: softDelete, Redirects: redirects}
}

// reconcileSortClusters sorts sources and clusters so newID assignment is deterministic.
func reconcileSortClusters(clusters []ClusterInput) []ClusterInput {
	cls := make([]ClusterInput, len(clusters))
	for i, c := range clusters {
		ids := append([]string(nil), c.SourceIDs...)
		sort.Strings(ids)
		cls[i] = ClusterInput{SourceIDs: ids}
	}
	sort.Slice(cls, func(i, j int) bool {
		if len(cls[i].SourceIDs) == 0 || len(cls[j].SourceIDs) == 0 {
			return len(cls[i].SourceIDs) > len(cls[j].SourceIDs)
		}
		return cls[i].SourceIDs[0] < cls[j].SourceIDs[0]
	})
	return cls
}

// reconcileOverlaps: overlaps[ci][oldID] = how many of cluster ci's sources
// currently belong to activity oldID; allOld is every old id in the bucket.
func reconcileOverlaps(cls []ClusterInput, existing map[string]string) ([]map[string]int, map[string]bool) {
	overlaps := make([]map[string]int, len(cls))
	allOld := map[string]bool{}
	for ci, c := range cls {
		ov := map[string]int{}
		for _, sid := range c.SourceIDs {
			if old, ok := existing[sid]; ok && old != "" {
				ov[old]++
				allOld[old] = true
			}
		}
		overlaps[ci] = ov
	}
	return overlaps, allOld
}

// reconcileBestOld picks each cluster's preferred old id = max overlap (tie → smallest id).
func reconcileBestOld(overlaps []map[string]int) ([]string, []int) {
	bestOld := make([]string, len(overlaps))
	bestCnt := make([]int, len(overlaps))
	for ci, ov := range overlaps {
		var bo string
		bc := 0
		for old, cnt := range ov {
			if cnt > bc || (cnt == bc && (bo == "" || old < bo)) {
				bo, bc = old, cnt
			}
		}
		bestOld[ci], bestCnt[ci] = bo, bc
	}
	return bestOld, bestCnt
}

// reconcileClaims: each old id is inherited by at most one cluster (largest overlap).
func reconcileClaims(bestOld []string, bestCnt []int) map[string]int {
	claimedBy := map[string]int{}
	for ci, old := range bestOld {
		if old == "" {
			continue
		}
		cur, taken := claimedBy[old]
		if !taken || bestCnt[ci] > bestCnt[cur] {
			claimedBy[old] = ci
		}
	}
	return claimedBy
}

// reconcileDissolved returns unclaimed old ids and their redirects to the
// plurality cluster (the one holding most of that id's sources; tie → lower ci).
func reconcileDissolved(overlaps []map[string]int, allOld map[string]bool, claimedBy map[string]int, assigned []string) ([]string, [][2]string) {
	var softDelete []string
	var redirects [][2]string
	for old := range allOld {
		if _, kept := claimedBy[old]; kept {
			continue // a cluster inherited it; not dissolved
		}
		softDelete = append(softDelete, old)
		bestCi, best := -1, -1
		for ci := range overlaps {
			if c := overlaps[ci][old]; c > best {
				bestCi, best = ci, c
			}
		}
		if bestCi >= 0 {
			redirects = append(redirects, [2]string{old, assigned[bestCi]})
		}
	}
	sort.Strings(softDelete)
	sort.Slice(redirects, func(i, j int) bool { return redirects[i][0] < redirects[j][0] })
	return softDelete, redirects
}
