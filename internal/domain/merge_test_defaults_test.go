package domain

// testMergePolicyFor returns rich provider-aware default policies for
// the merge-engine tests. These are EXAMPLES of what an operator might
// configure for a deployment that primarily integrates Garmin + Strava
// (+ Polar for swims) — they're NOT production defaults. The domain
// itself ships with the trivial AnyProvider-only policy
// (DefaultMergePolicyFor in merge.go) because provider preference is
// instance-level configuration, not domain.
//
// Used as a resolver in merge_engine_test.go cases that exercise
// prioritisation logic. The tests are checking engine behaviour given
// THIS policy, not asserting that the domain ships this policy.
func testMergePolicyFor(t ActivityType) MergePolicy {
	switch t {
	case ActivityTypeRide:
		return MergePolicy{
			DefaultPriority: []string{"garmin", "strava", AnyProvider},
			Overrides: map[FieldGroup][]string{
				FieldGroupClassification: {"strava", "garmin", AnyProvider},
				FieldGroupTitle:          {"strava", AnyProvider},
				FieldGroupDescription:    {"strava", AnyProvider},
			},
		}
	case ActivityTypeRun:
		return MergePolicy{
			DefaultPriority: []string{"garmin", "polar", "strava", AnyProvider},
			Overrides: map[FieldGroup][]string{
				FieldGroupClassification: {"strava", AnyProvider},
				FieldGroupTitle:          {"strava", AnyProvider},
				FieldGroupDescription:    {"strava", AnyProvider},
			},
		}
	case ActivityTypeSwim:
		return MergePolicy{
			DefaultPriority: []string{"garmin", "polar", AnyProvider},
			Overrides: map[FieldGroup][]string{
				FieldGroupClassification: {"strava", AnyProvider},
			},
		}
	}
	return MergePolicy{
		DefaultPriority: []string{AnyProvider},
	}
}
