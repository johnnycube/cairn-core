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
	n := len(clusters)

	// Sort sources and clusters so newID assignment is deterministic.
	cls := make([]ClusterInput, n)
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

	// overlaps[ci][oldID] = how many of cluster ci's sources currently belong to
	// activity oldID. Also collect the set of all old ids touched in the bucket.
	overlaps := make([]map[string]int, n)
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

	// Each cluster's preferred old id = max overlap (tie → smallest id).
	bestOld := make([]string, n)
	bestCnt := make([]int, n)
	for ci := range cls {
		var bo string
		bc := 0
		for old, cnt := range overlaps[ci] {
			if cnt > bc || (cnt == bc && (bo == "" || old < bo)) {
				bo, bc = old, cnt
			}
		}
		bestOld[ci], bestCnt[ci] = bo, bc
	}

	// Claim: each old id is inherited by at most one cluster (largest overlap).
	claimedBy := map[string]int{}
	for ci := range cls {
		old := bestOld[ci]
		if old == "" {
			continue
		}
		cur, taken := claimedBy[old]
		if !taken || bestCnt[ci] > bestCnt[cur] {
			claimedBy[old] = ci
		}
	}

	// Assign ids: inheritors keep their claimed old id; everyone else gets fresh.
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

	// Plurality target per old id (for redirecting dissolved ids): the cluster
	// holding the most of that old id's sources (tie → lower ci).
	plurality := map[string]int{}
	for old := range allOld {
		bestCi, best := -1, -1
		for ci := range cls {
			if c := overlaps[ci][old]; c > best {
				bestCi, best = ci, c
			}
		}
		plurality[old] = bestCi
	}

	kept := map[string]bool{}
	for old := range claimedBy {
		kept[old] = true
	}

	var softDelete []string
	var redirects [][2]string
	for old := range allOld {
		if kept[old] {
			continue // a cluster inherited it; not dissolved
		}
		softDelete = append(softDelete, old)
		if ci := plurality[old]; ci >= 0 {
			redirects = append(redirects, [2]string{old, assigned[ci]})
		}
	}
	sort.Strings(softDelete)
	sort.Slice(redirects, func(i, j int) bool { return redirects[i][0] < redirects[j][0] })

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
