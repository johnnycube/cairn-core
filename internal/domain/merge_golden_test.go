package domain

import (
	"sort"
	"testing"
	"time"
)

// merge_golden_test.go pins the CURRENT merge-engine output for a fixed,
// deterministic multi-source input. It is the baseline the merge-layer rewrite
// (docs/merge-layer-rewrite-plan.md) must not silently regress — in particular
// Phase 7, which changes the MergeProvenance shape to carry decided-by/timestamp
// metadata, must keep producing the SAME per-field-group winners this snapshot
// records. Behavioural assertions live in merge_engine_test.go; this file is the
// whole-result golden snapshot.

// goldenInput returns the canonical two-source ride (Strava imported an hour
// before a higher-fidelity Garmin import) used by the golden snapshot. The
// payload bodies come from the shared merge_engine_test.go builders so the
// snapshot tracks the same fixtures the behavioural tests use.
func goldenInput(t *testing.T) (strava, garmin ActivitySource, actID ActivityID) {
	t.Helper()
	user := makeUserID("golden")
	actID = makeActivityID("golden-ride")
	strava = makeStravaRide("golden-s", user, actID, -1*time.Hour)
	garmin = makeGarminRide("golden-g", user, actID, 0, nil)
	return strava, garmin, actID
}

func TestMerge_Golden_RideProvenanceSnapshot(t *testing.T) {
	strava, garmin, actID := goldenInput(t)
	resolver := func(at ActivityType) MergePolicy { return testMergePolicyFor(at) }

	merged, err := Merge(actID, []ActivitySource{strava, garmin}, resolver, fixedNow)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Label the two source IDs so the golden map reads provider-by-provider.
	label := map[SourceID]string{strava.ID: "strava", garmin.ID: "garmin"}

	// GOLDEN: every field group's winning provider for the canonical ride under
	// the test ride policy (default [garmin, strava, _any]; classification/title/
	// description overridden to prefer strava). Groups with no provider (swim,
	// laps here) MUST be absent from the provenance map.
	want := map[FieldGroup]string{
		FieldGroupClassification: "strava", // override prefers strava
		FieldGroupTitle:          "strava", // override [strava, _any]
		FieldGroupDescription:    "strava", // override [strava, _any]; garmin desc empty anyway
		FieldGroupTime:           "garmin",
		FieldGroupDuration:       "garmin",
		FieldGroupDistance:       "garmin",
		FieldGroupElevation:      "garmin",
		FieldGroupSpeed:          "garmin",
		FieldGroupHeartRate:      "garmin",
		FieldGroupPower:          "garmin",
		FieldGroupCadence:        "garmin", // only garmin has cadence
		FieldGroupTemperature:    "garmin", // only garmin has temperature
		FieldGroupCalories:       "garmin",
		FieldGroupTrainingLoad:   "garmin", // only garmin has TSS/IF
		FieldGroupGPSTrack:       "garmin", // primary stream source
	}

	// Compare the full provenance map both ways so a NEW unexpected winner or a
	// DROPPED group both fail.
	got := map[FieldGroup]string{}
	for g, sid := range merged.MergeProvenance {
		lbl, ok := label[sid]
		if !ok {
			t.Errorf("group %s won by unknown source %s", g, sid)
			continue
		}
		got[g] = lbl
	}

	for g, wantLbl := range want {
		if gotLbl, ok := got[g]; !ok {
			t.Errorf("group %s: missing from provenance, want winner %s", g, wantLbl)
		} else if gotLbl != wantLbl {
			t.Errorf("group %s: winner = %s, want %s", g, gotLbl, wantLbl)
		}
	}
	for g := range got {
		if _, ok := want[g]; !ok {
			t.Errorf("group %s: unexpected winner %s (not in golden)", g, got[g])
		}
	}

	// Absent groups (no source provides them) stay absent.
	for _, g := range []FieldGroup{FieldGroupSwim, FieldGroupLaps} {
		if _, ok := merged.MergeProvenance.Winner(g); ok {
			t.Errorf("group %s: unexpectedly present in provenance", g)
		}
	}

	// GOLDEN: the merged field bodies lifted from the winners.
	if merged.Type != ActivityTypeRide {
		t.Errorf("Type = %q, want Ride", merged.Type)
	}
	if merged.Title != "Strava Lunch Ride" {
		t.Errorf("Title = %q, want %q", merged.Title, "Strava Lunch Ride")
	}
	if merged.Description != "Felt good" {
		t.Errorf("Description = %q, want %q", merged.Description, "Felt good")
	}
	if got := Deref(merged.Summary.DistanceM); got != 30050 {
		t.Errorf("DistanceM = %v, want 30050 (garmin)", got)
	}
	if got := Deref(merged.Summary.AvgPowerW); got != 195 {
		t.Errorf("AvgPowerW = %d, want 195 (garmin)", got)
	}
	if got := Deref(merged.Summary.AvgHeartRateBpm); got != 146 {
		t.Errorf("AvgHeartRateBpm = %d, want 146 (garmin)", got)
	}
	if got := Deref(merged.Summary.CaloriesKcal); got != 680 {
		t.Errorf("CaloriesKcal = %d, want 680 (garmin)", got)
	}
	if got := Deref(merged.Summary.AvgCadence); got != 85 {
		t.Errorf("AvgCadence = %d, want 85 (garmin)", got)
	}
	if got := Deref(merged.Summary.TSS); got != 72.0 {
		t.Errorf("TSS = %v, want 72 (garmin)", got)
	}
	if merged.PrimaryStreamSourceID == nil || *merged.PrimaryStreamSourceID != garmin.ID {
		t.Errorf("PrimaryStreamSourceID = %v, want garmin %s", merged.PrimaryStreamSourceID, garmin.ID)
	}
	if !merged.StartTime.Equal(rideStart) {
		t.Errorf("StartTime = %v, want %v", merged.StartTime, rideStart)
	}
	if !merged.EndTime.Equal(rideStart.Add(time.Hour)) {
		t.Errorf("EndTime = %v, want %v", merged.EndTime, rideStart.Add(time.Hour))
	}
}

// TestMerge_Golden_Stable re-runs the golden merge and asserts the winner set is
// identical across runs and independent of input order — the determinism the
// whole re-derivation model depends on.
func TestMerge_Golden_Stable(t *testing.T) {
	strava, garmin, actID := goldenInput(t)
	resolver := func(at ActivityType) MergePolicy { return testMergePolicyFor(at) }

	a, err := Merge(actID, []ActivitySource{strava, garmin}, resolver, fixedNow)
	if err != nil {
		t.Fatalf("merge a: %v", err)
	}
	b, err := Merge(actID, []ActivitySource{garmin, strava}, resolver, fixedNow)
	if err != nil {
		t.Fatalf("merge b: %v", err)
	}

	if snap := provenanceSnapshot(a); snap != provenanceSnapshot(b) {
		t.Errorf("provenance differs under input reorder:\n a=%s\n b=%s", snap, provenanceSnapshot(b))
	}
}

// provenanceSnapshot renders a provenance map as a deterministic, sorted string
// so two merges can be compared regardless of Go map iteration order.
func provenanceSnapshot(a Activity) string {
	keys := make([]string, 0, len(a.MergeProvenance))
	for g := range a.MergeProvenance {
		keys = append(keys, string(g))
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, '=')
		b = append(b, a.MergeProvenance[FieldGroup(k)].String()...)
		b = append(b, ';')
	}
	return string(b)
}
