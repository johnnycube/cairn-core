package domain

// ---------------------------------------------------------------------------
// FieldGroup
//
// The merge engine resolves the Activity field-by-field-group rather than
// field-by-field. A group is a coherent bundle (e.g. AvgHeartRate + MaxHeartRate
// always come from the same source — otherwise inconsistent.)
//
// The order of constants here is the order in which groups are resolved
// by the merge engine. Earlier groups can influence later groups (e.g.
// FieldGroupClassification resolves first because the MergePolicy is
// keyed by ActivityType).
// ---------------------------------------------------------------------------

type FieldGroup string

const (
	// FieldGroupClassification picks Type, Discipline, IsVirtual, IsEbike,
	// IsCommute, IsRace, CustomSubtype. Resolved first because the chosen
	// ActivityType selects which MergePolicy applies to subsequent groups.
	FieldGroupClassification FieldGroup = "classification"

	// FieldGroupTime picks StartTime, EndTime, Timezone.
	FieldGroupTime FieldGroup = "time"

	// FieldGroupDuration picks ElapsedDuration, MovingDuration.
	FieldGroupDuration FieldGroup = "duration"

	// FieldGroupTitle picks the human-readable title. Treated separately
	// from Description because users frequently edit one but not the other.
	FieldGroupTitle FieldGroup = "title"

	// FieldGroupDescription picks the long-form description.
	FieldGroupDescription FieldGroup = "description"

	FieldGroupDistance     FieldGroup = "distance"
	FieldGroupElevation    FieldGroup = "elevation"
	FieldGroupSpeed        FieldGroup = "speed"
	FieldGroupHeartRate    FieldGroup = "heart_rate"
	FieldGroupPower        FieldGroup = "power"
	FieldGroupCadence      FieldGroup = "cadence"
	FieldGroupTemperature  FieldGroup = "temperature"
	FieldGroupCalories     FieldGroup = "calories"
	FieldGroupTrainingLoad FieldGroup = "training_load"
	FieldGroupSwim         FieldGroup = "swim"

	// FieldGroupGPSTrack chooses the primary stream-bearing source — does
	// not write any fields on the Activity directly; instead the merge
	// engine sets Activity.PrimaryStreamSourceID to the winner's ID.
	FieldGroupGPSTrack FieldGroup = "gps_track"

	// FieldGroupLaps chooses which source's lap segmentation the UI renders.
	// Like GPSTrack, this picks an ID rather than writing fields.
	FieldGroupLaps FieldGroup = "laps"
)

// AllFieldGroups is the deterministic iteration order for the merge engine.
// Classification first (because policy depends on type), then time/duration
// (because every later group needs a coherent time bound), then the rest in
// dependency-free order.
var AllFieldGroups = []FieldGroup{
	FieldGroupClassification,
	FieldGroupTime,
	FieldGroupDuration,
	FieldGroupTitle,
	FieldGroupDescription,
	FieldGroupDistance,
	FieldGroupElevation,
	FieldGroupSpeed,
	FieldGroupHeartRate,
	FieldGroupPower,
	FieldGroupCadence,
	FieldGroupTemperature,
	FieldGroupCalories,
	FieldGroupTrainingLoad,
	FieldGroupSwim,
	FieldGroupGPSTrack,
	FieldGroupLaps,
}

// ---------------------------------------------------------------------------
// MergePolicy
//
// For a given ActivityType: a default priority list of providers, plus
// per-field-group overrides. The merge engine walks the priority list
// for each group and picks the first source whose Provides(group)=true.
//
// The priority list contains provider IDs (matching ActivitySource.Provider).
// A special sentinel "_any" matches any provider not otherwise mentioned —
// useful for "prefer Garmin, then anything else".
// ---------------------------------------------------------------------------

type MergePolicy struct {
	// Default priority list applied to every field group not in Overrides.
	DefaultPriority []string

	// Per-field-group overrides. If a group has an entry here it is used
	// in place of DefaultPriority.
	Overrides map[FieldGroup][]string
}

// PriorityFor returns the priority list applicable to field group g for
// this policy. If no override exists, returns DefaultPriority.
func (p MergePolicy) PriorityFor(g FieldGroup) []string {
	if list, ok := p.Overrides[g]; ok && len(list) > 0 {
		return list
	}
	return p.DefaultPriority
}

// AnyProvider is the wildcard token usable inside priority lists. Sources
// not explicitly named match this token.
const AnyProvider = "_any"

// DefaultMergePolicyFor returns the safe-permissive fallback policy:
// no provider preference, just AnyProvider as the catch-all. The
// merge engine picks the first available source for each field.
//
// Rich provider-aware priority lists (e.g. "for rides, prefer Garmin's
// power data over Strava's") are an INSTANCE-LEVEL CONFIG concern, not
// a domain concern — they vary by deployment, evolve as new providers
// are added, and shouldn't require a code change to adjust. They live in:
//
//   - UserSettings.MergePolicyByActivityType  — per-user overrides
//   - instance_settings.merge_defaults_json   — instance-wide defaults
//     (future migration)
//
// Until per-instance defaults ship, deployments that want richer
// defaults can set them at user-creation time via the admin endpoint
// or seed-data SQL.
//
// Tests that need rich priorities to exercise the engine's prioritisation
// logic should construct the MergePolicy inline (or use the
// testMergePolicyFor helper in merge_test_defaults_test.go) — they're
// testing engine BEHAVIOUR given a policy, not the policy data itself.
func DefaultMergePolicyFor(_ ActivityType) MergePolicy {
	return MergePolicy{
		DefaultPriority: []string{AnyProvider},
	}
}

// ---------------------------------------------------------------------------
// MergeProvenance
//
// Records which source won each field-group for an Activity. Persisted
// alongside the Activity (jsonb column activities.merge_provenance).
// Used by the UI to render "this distance is from Garmin, this title is
// from Strava" affordances on the activity-detail page.
// ---------------------------------------------------------------------------

type MergeProvenance map[FieldGroup]SourceID

// Set records that source s won field group g.
func (m MergeProvenance) Set(g FieldGroup, s SourceID) {
	m[g] = s
}

// Winner returns the source ID that won group g, or (zero, false) if no
// source provided it.
func (m MergeProvenance) Winner(g FieldGroup) (SourceID, bool) {
	id, ok := m[g]
	return id, ok
}

// Clone returns a deep copy. The merge engine never mutates a provided
// MergeProvenance; it builds a fresh one per recomputation.
func (m MergeProvenance) Clone() MergeProvenance {
	out := make(MergeProvenance, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
