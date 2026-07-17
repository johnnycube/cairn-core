package match

import (
	"strings"
	"testing"
)

// These are sanity checks that keep the fixture corpus honest and the package
// compiling. The REAL matcher tests (pairwise scoring, union-find clustering,
// precision/recall against this corpus) arrive in Phase 3 — see
// docs/merge-layer-rewrite-plan.md.

func TestCorpus_NonEmpty(t *testing.T) {
	c := Corpus()
	if len(c) < 8 {
		t.Fatalf("Corpus() returned %d fixtures, want at least 8", len(c))
	}
}

func TestCorpus_LabelsValid(t *testing.T) {
	for _, f := range Corpus() {
		if _, ok := ValidLabels[f.Label]; !ok {
			t.Errorf("fixture %q: invalid label %q", f.Name, f.Label)
		}
	}
}

func TestCorpus_NamesUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, f := range Corpus() {
		if f.Name == "" {
			t.Error("fixture with empty Name")
			continue
		}
		if _, dup := seen[f.Name]; dup {
			t.Errorf("duplicate fixture name %q", f.Name)
		}
		seen[f.Name] = struct{}{}
	}
}

// "same" and "indoor_nogps" pairs are, by construction, the SAME real-world
// activity — so their two records must carry compatible sport classes. We use
// a deliberately loose compatibility check (exact match or one being a prefix
// of the other, case-insensitively) because the real fuzzy sport-compat map is
// Phase-3 work; this just guards against an obviously broken fixture (a Ride
// labeled the same as a Run).
func TestCorpus_SamePairsHaveCompatibleSport(t *testing.T) {
	for _, f := range Corpus() {
		if f.Label != LabelSame && f.Label != LabelIndoorNoGPS {
			continue
		}
		if !sportCompatible(f.A.SportClass, f.B.SportClass) {
			t.Errorf("fixture %q labeled %q but sport classes %q / %q are not compatible",
				f.Name, f.Label, f.A.SportClass, f.B.SportClass)
		}
	}
}

// indoor_nogps fixtures must genuinely have no GPS on either side — that's the
// property that makes them the hard case.
func TestCorpus_IndoorNoGPSHasNoGPS(t *testing.T) {
	for _, f := range Corpus() {
		if f.Label != LabelIndoorNoGPS {
			continue
		}
		if f.A.StartLat != nil || f.A.StartLng != nil || f.B.StartLat != nil || f.B.StartLng != nil {
			t.Errorf("fixture %q labeled indoor_nogps but carries GPS coordinates", f.Name)
		}
	}
}

func sportCompatible(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la == lb {
		return true
	}
	return strings.HasPrefix(la, lb) || strings.HasPrefix(lb, la)
}
