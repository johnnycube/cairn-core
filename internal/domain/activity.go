package domain

import "time"

// ---------------------------------------------------------------------------
// Activity
//
// The merged view of one workout. An Activity is computed deterministically
// from one or more ActivitySource rows by RecomputeActivityFromSources.
// Nullable summary fields use *T to distinguish "the merge could not find
// any source providing this field" from "the source said zero".
// ---------------------------------------------------------------------------

type Activity struct {
	// Identity
	ID     ActivityID
	UserID UserID

	// Classification
	Type          ActivityType
	Discipline    Discipline // DisciplineNone when unset
	IsVirtual     bool
	IsEbike       bool
	IsCommute     bool
	IsRace        bool
	CustomSubtype string

	// User-editable descriptive fields
	Title       string
	Description string

	// Time
	StartTime       time.Time
	EndTime         time.Time
	ElapsedDuration time.Duration
	MovingDuration  time.Duration
	Timezone        string // IANA name, e.g. "Europe/Berlin"

	// Summary metrics. Pointer = "not provided by any merged source".
	Summary ActivitySummary

	// Provenance: which source won each field-group.
	MergeProvenance MergeProvenance

	// The source whose stream renders on the activity page by default.
	// Selected by the merge engine; usually the highest-priority
	// stream-bearing source.
	PrimaryStreamSourceID *SourceID

	MergedAt time.Time

	GearID  *GearID
	Tags    []string
	Privacy ActivityPrivacy

	// Start location, denormalised for the feed/detail subtitle. StartLat/
	// StartLng cache the first GPS point of the primary stream; StartPlace is
	// the reverse-geocoded place name. StartPlace == "" with non-nil coords
	// means "geocoded, no place found"; see migration 00025. These are managed
	// out-of-band (SetStartLocation), not by the merge engine, so a re-merge
	// never clobbers a resolved place.
	StartLat   *float64
	StartLng   *float64
	StartPlace string

	// HiddenByAdmin hides the activity from all non-owner read paths
	// (moderation). Independent of Privacy and soft-delete.
	HiddenByAdmin bool

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// ActivitySummary holds the post-merge denormalised summary metrics. Carved
// out as a sub-struct so adapters can serialise/deserialise it as a single
// unit and tests can construct it without naming Activity's full field list.
type ActivitySummary struct {
	// Distance / elevation
	DistanceM      *float64
	ElevationGainM *float64
	ElevationLossM *float64
	MinElevationM  *float64
	MaxElevationM  *float64

	// Speed
	AvgSpeedMps *float64
	MaxSpeedMps *float64

	// Heart rate
	AvgHeartRateBpm *int
	MaxHeartRateBpm *int

	// Power
	AvgPowerW        *int
	MaxPowerW        *int
	NormalizedPowerW *int

	// Cadence
	AvgCadence *int
	MaxCadence *int

	// Temperature
	AvgTemperatureC *float64
	MinTemperatureC *float64
	MaxTemperatureC *float64

	// Calories / training load
	CaloriesKcal    *int
	TSS             *float64
	IntensityFactor *float64

	// Swim-specific
	PoolLengthM  *int
	TotalStrokes *int
}

// IsDeleted reports whether the activity is soft-deleted.
func (a *Activity) IsDeleted() bool {
	return a.DeletedAt != nil
}

// Validate enforces invariants the merge engine should always produce.
// Adapter code calls this before persisting.
func (a *Activity) Validate() error {
	if a.ID == (ActivityID{}) {
		return ErrActivityMissingID
	}
	if a.UserID == (UserID{}) {
		return ErrActivityMissingUserID
	}
	if !a.Type.Valid() {
		return ErrActivityInvalidType
	}
	if !a.Discipline.CompatibleWith(a.Type) {
		return ErrActivityDisciplineIncompatible
	}
	if !a.Privacy.Valid() {
		return ErrActivityInvalidPrivacy
	}
	if a.EndTime.Before(a.StartTime) {
		return ErrActivityTimeOrder
	}
	if a.ElapsedDuration < 0 || a.MovingDuration < 0 {
		return ErrActivityNegativeDuration
	}
	if a.MovingDuration > a.ElapsedDuration {
		return ErrActivityMovingExceedsElapsed
	}
	if a.Timezone == "" {
		return ErrActivityMissingTimezone
	}
	return nil
}

// BestEffortHistoryItem is one activity's value for a specific best-effort
// bucket (metric + window), for the best-effort progression view.
type BestEffortHistoryItem struct {
	ActivityID    ActivityID
	Title         string
	StartTime     time.Time
	AchievedValue float64
}
