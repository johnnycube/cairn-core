package domain

import (
	"testing"
	"time"
)

// A manual per-field source pin overrides the cascade winner and is reflected in
// provenance + the merged field value.
func TestMergeWithOverrides_PinsField(t *testing.T) {
	user := makeUserID("alice")
	actID := makeActivityID("ov-ride")
	strava := makeStravaRide("s1", user, actID, -1*time.Hour)
	garmin := makeGarminRide("g1", user, actID, 0, nil)
	resolver := func(at ActivityType) MergePolicy { return testMergePolicyFor(at) }

	// Without override: ride policy gives Power to Garmin.
	base, err := Merge(actID, []ActivitySource{strava, garmin}, resolver, fixedNow)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if w, _ := base.MergeProvenance.Winner(FieldGroupPower); w != garmin.ID {
		t.Fatalf("precondition: power winner = %s, want garmin", w)
	}

	// Pin Power to Strava.
	ov := map[FieldGroup]SourceID{FieldGroupPower: strava.ID}
	merged, err := MergeWithOverrides(actID, []ActivitySource{strava, garmin}, resolver, ov, fixedNow)
	if err != nil {
		t.Fatalf("merge with override: %v", err)
	}
	if w, _ := merged.MergeProvenance.Winner(FieldGroupPower); w != strava.ID {
		t.Errorf("power winner = %s, want strava (pinned)", w)
	}
	if got := Deref(merged.Summary.AvgPowerW); got != 180 {
		t.Errorf("AvgPowerW = %d, want 180 (strava, pinned)", got)
	}
	// Non-pinned groups are unaffected (distance still Garmin).
	if w, _ := merged.MergeProvenance.Winner(FieldGroupDistance); w != garmin.ID {
		t.Errorf("distance winner = %s, want garmin (unpinned)", w)
	}
}

// A pin for a source that doesn't provide the group (or isn't present) is
// ignored — the cascade decides.
func TestMergeWithOverrides_IgnoredWhenSourceLacksField(t *testing.T) {
	user := makeUserID("alice")
	actID := makeActivityID("ov-ride2")
	strava := makeStravaRide("s1", user, actID, -1*time.Hour)
	garmin := makeGarminRide("g1", user, actID, 0, nil)
	// Strava has no cadence; pin cadence to strava → must be ignored, Garmin wins.
	strava.Parsed.Summary.AvgCadence = nil
	strava.Parsed.Summary.MaxCadence = nil
	resolver := func(at ActivityType) MergePolicy { return testMergePolicyFor(at) }

	ov := map[FieldGroup]SourceID{FieldGroupCadence: strava.ID}
	merged, err := MergeWithOverrides(actID, []ActivitySource{strava, garmin}, resolver, ov, fixedNow)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if w, _ := merged.MergeProvenance.Winner(FieldGroupCadence); w != garmin.ID {
		t.Errorf("cadence winner = %s, want garmin (pin ignored — strava has no cadence)", w)
	}
}
