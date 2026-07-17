package reconcile

import (
	"fmt"
	"testing"
)

// seqID returns a deterministic id minter ("new-1", "new-2", ...).
func seqID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("new-%d", n) }
}

func decisionFor(p Plan, sid string) (Decision, bool) {
	for _, d := range p.Decisions {
		for _, s := range d.SourceIDs {
			if s == sid {
				return d, true
			}
		}
	}
	return Decision{}, false
}

// Stable identity: a new source joining an existing single-source activity
// inherits that activity's id.
func TestReconcile_AttachInheritsID(t *testing.T) {
	// cluster {s1, s2}; s1 already belongs to act-A, s2 is new.
	clusters := []ClusterInput{{SourceIDs: []string{"s1", "s2"}}}
	existing := map[string]string{"s1": "act-A"}
	p := Reconcile(clusters, existing, seqID())

	if len(p.Decisions) != 1 {
		t.Fatalf("want 1 decision, got %d", len(p.Decisions))
	}
	d := p.Decisions[0]
	if d.ActivityID != "act-A" || !d.Inherited {
		t.Errorf("cluster should inherit act-A, got %+v", d)
	}
	if len(p.SoftDelete) != 0 {
		t.Errorf("nothing should be deleted, got %v", p.SoftDelete)
	}
}

// Brand-new activity: a cluster with no existing assignment gets a fresh id.
func TestReconcile_NewActivity(t *testing.T) {
	p := Reconcile([]ClusterInput{{SourceIDs: []string{"s1"}}}, map[string]string{}, seqID())
	d := p.Decisions[0]
	if d.Inherited || d.ActivityID != "new-1" {
		t.Errorf("expected fresh id new-1, got %+v", d)
	}
}

// Split: act-A had {s1,s2,s3}; matcher now says {s1,s2} and {s3} are different.
// The larger piece keeps act-A; the smaller gets a fresh id. Nothing deleted.
func TestReconcile_Split(t *testing.T) {
	clusters := []ClusterInput{
		{SourceIDs: []string{"s1", "s2"}},
		{SourceIDs: []string{"s3"}},
	}
	existing := map[string]string{"s1": "act-A", "s2": "act-A", "s3": "act-A"}
	p := Reconcile(clusters, existing, seqID())

	dBig, _ := decisionFor(p, "s1")
	dSmall, _ := decisionFor(p, "s3")
	if dBig.ActivityID != "act-A" || !dBig.Inherited {
		t.Errorf("larger piece should keep act-A, got %+v", dBig)
	}
	if dSmall.ActivityID == "act-A" || dSmall.Inherited {
		t.Errorf("smaller piece should get a fresh id, got %+v", dSmall)
	}
	if len(p.SoftDelete) != 0 {
		t.Errorf("split must not delete act-A, got %v", p.SoftDelete)
	}
}

// Merge: s1∈act-A, s2∈act-B; matcher now says they're the same. One id wins
// (the one with the larger overlap; here both have 1, tie → smaller id act-A),
// the other is dissolved + redirected.
func TestReconcile_Merge(t *testing.T) {
	clusters := []ClusterInput{{SourceIDs: []string{"s1", "s2"}}}
	existing := map[string]string{"s1": "act-A", "s2": "act-B"}
	p := Reconcile(clusters, existing, seqID())

	d := p.Decisions[0]
	if d.ActivityID != "act-A" || !d.Inherited {
		t.Errorf("merged cluster should keep act-A (tie→smaller id), got %+v", d)
	}
	if len(p.SoftDelete) != 1 || p.SoftDelete[0] != "act-B" {
		t.Errorf("act-B should be dissolved, got %v", p.SoftDelete)
	}
	if len(p.Redirects) != 1 || p.Redirects[0] != [2]string{"act-B", "act-A"} {
		t.Errorf("expected redirect act-B→act-A, got %v", p.Redirects)
	}
}

// Merge favors the larger overlap: act-A contributes 2 sources, act-B 1 → the
// merged cluster keeps act-A even though act-B's id is... here act-A is also
// smaller, so make act-B the larger contributor to prove count beats id order.
func TestReconcile_MergeFavorsLargerOverlap(t *testing.T) {
	clusters := []ClusterInput{{SourceIDs: []string{"s1", "s2", "s3"}}}
	// act-Z has two of the sources, act-A has one. act-Z must win despite "A"<"Z".
	existing := map[string]string{"s1": "act-A", "s2": "act-Z", "s3": "act-Z"}
	p := Reconcile(clusters, existing, seqID())
	d := p.Decisions[0]
	if d.ActivityID != "act-Z" {
		t.Errorf("cluster should keep act-Z (2 sources) over act-A (1), got %+v", d)
	}
	if len(p.SoftDelete) != 1 || p.SoftDelete[0] != "act-A" {
		t.Errorf("act-A should be dissolved, got %v", p.SoftDelete)
	}
}

// Determinism: reordering clusters and source lists yields the same plan.
func TestReconcile_Deterministic(t *testing.T) {
	existing := map[string]string{"s1": "act-A", "s2": "act-A", "s3": "act-B"}
	a := Reconcile([]ClusterInput{{[]string{"s1", "s2"}}, {[]string{"s3"}}}, existing, seqID())
	b := Reconcile([]ClusterInput{{[]string{"s3"}}, {[]string{"s2", "s1"}}}, existing, seqID())

	norm := func(p Plan) string {
		s := ""
		for _, d := range p.Decisions {
			s += fmt.Sprintf("%v=%s;", d.SourceIDs, idClass(d))
		}
		return s
	}
	if norm(a) != norm(b) {
		t.Errorf("non-deterministic:\n a=%s\n b=%s", norm(a), norm(b))
	}
}

// idClass abstracts the concrete fresh-id value so determinism comparison
// ignores the (deterministic but positional) new-N numbering and compares
// inherited-vs-fresh + which old id.
func idClass(d Decision) string {
	if d.Inherited {
		return "inherit:" + d.InheritedFrom
	}
	return "fresh"
}
