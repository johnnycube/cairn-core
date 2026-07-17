package domain

import "time"

// FieldSourceOverride pins which source wins a field group for one activity.
// Re-applied on every recompute, so it survives re-derivation.
type FieldSourceOverride struct {
	ActivityID ActivityID
	FieldKey   FieldGroup
	SourceID   SourceID
}

// SourceDenylistEntry marks a detached identity that must not re-attach on
// re-push (Gap 6). ExternalAccountID is nil for manual uploads.
type SourceDenylistEntry struct {
	UserID            UserID
	Provider          string
	ExternalAccountID *ExternalAccountID
	ExternalID        string
	Reason            string
}

// ClassificationOverride is a per-activity overlay of user-set classification
// fields. Unlike FieldSourceOverride (which pins a whole field group to a
// source), this holds literal user values applied AFTER the merge, so a user
// can correct a single field (e.g. mark a ride as a commute, or change the
// sport) without freezing the rest of the classification or polluting the
// source list. A nil pointer means "not overridden — use the merged value".
// Re-applied on every recompute, so the edit survives re-derivation.
type ClassificationOverride struct {
	ActivityID    ActivityID
	Type          *ActivityType
	Discipline    *Discipline
	IsVirtual     *bool
	IsEbike       *bool
	IsCommute     *bool
	IsRace        *bool
	CustomSubtype *string

	// Summary-metric overrides — hand-corrections to merged numbers (e.g. a
	// GPS-drift distance). Applied post-merge; overriding distance or moving
	// time re-derives avg speed.
	DistanceM      *float64
	ElevationGainM *float64
	MovingDuration *time.Duration
}

// Empty reports whether no field is overridden (so the row can be dropped).
func (o ClassificationOverride) Empty() bool {
	return o.Type == nil && o.Discipline == nil && o.IsVirtual == nil &&
		o.IsEbike == nil && o.IsCommute == nil && o.IsRace == nil &&
		o.CustomSubtype == nil && o.DistanceM == nil && o.ElevationGainM == nil &&
		o.MovingDuration == nil
}

// ApplyTo overlays the set fields onto a merged Activity.
func (o ClassificationOverride) ApplyTo(a *Activity) {
	if o.Type != nil {
		a.Type = *o.Type
	}
	if o.Discipline != nil {
		a.Discipline = *o.Discipline
	}
	if o.IsVirtual != nil {
		a.IsVirtual = *o.IsVirtual
	}
	if o.IsEbike != nil {
		a.IsEbike = *o.IsEbike
	}
	if o.IsCommute != nil {
		a.IsCommute = *o.IsCommute
	}
	if o.IsRace != nil {
		a.IsRace = *o.IsRace
	}
	if o.CustomSubtype != nil {
		a.CustomSubtype = *o.CustomSubtype
	}
	if o.DistanceM != nil {
		a.Summary.DistanceM = o.DistanceM
	}
	if o.ElevationGainM != nil {
		a.Summary.ElevationGainM = o.ElevationGainM
	}
	if o.MovingDuration != nil {
		a.MovingDuration = *o.MovingDuration
	}
	// Re-derive avg speed when distance or moving time was overridden, so the
	// headline pace/speed stays consistent with the corrected numbers.
	if (o.DistanceM != nil || o.MovingDuration != nil) &&
		a.Summary.DistanceM != nil && a.MovingDuration > 0 {
		spd := *a.Summary.DistanceM / a.MovingDuration.Seconds()
		a.Summary.AvgSpeedMps = &spd
	}
}
