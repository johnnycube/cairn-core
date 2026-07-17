package domain

import (
	"fmt"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Athlete physiology profile
//
// Athletes have physiological values (FTP, threshold heart rate, body weight,
// …) that drift over time. Rather than store a single "current" number, each
// value is a time series: one (value, effective_date) entry per measurement.
// Calculations resolve the value AS OF a given date by linear interpolation
// between the two bracketing entries, clamping to the nearest endpoint outside
// the measured range. This is what lets a re-compute of a 2024 activity use the
// athlete's 2024 FTP, not today's.
// ---------------------------------------------------------------------------

// AthleteMetricKey identifies one physiological quantity. The value's unit is
// fixed per key (see AthleteMetricSpec).
type AthleteMetricKey string

const (
	AthleteFTPWatts      AthleteMetricKey = "ftp_watts"      // functional threshold power
	AthleteThresholdHR   AthleteMetricKey = "threshold_hr"   // lactate-threshold HR (bpm)
	AthleteMaxHR         AthleteMetricKey = "max_hr"         // observed max HR (bpm)
	AthleteRestingHR     AthleteMetricKey = "resting_hr"     // resting HR (bpm)
	AthleteWeightKg      AthleteMetricKey = "weight_kg"      // body weight (kg)
	AthleteHeightCm      AthleteMetricKey = "height_cm"      // height (cm)
	AthleteThresholdPace AthleteMetricKey = "threshold_pace" // run threshold pace (sec/km)
)

// AthleteMetricSpec describes a metric for validation + UI rendering.
type AthleteMetricSpec struct {
	Key   AthleteMetricKey
	Label string
	Unit  string
	Min   float64
	Max   float64
}

// AthleteMetricSpecs is the canonical, ordered registry of supported metrics.
// The UI iterates this; validation uses the Min/Max bounds.
var AthleteMetricSpecs = []AthleteMetricSpec{
	{AthleteFTPWatts, "FTP", "W", 30, 700},
	{AthleteThresholdHR, "Threshold HR", "bpm", 80, 230},
	{AthleteMaxHR, "Max HR", "bpm", 100, 240},
	{AthleteRestingHR, "Resting HR", "bpm", 25, 120},
	{AthleteWeightKg, "Weight", "kg", 25, 250},
	{AthleteHeightCm, "Height", "cm", 100, 250},
	{AthleteThresholdPace, "Threshold pace", "sec/km", 120, 900},
}

// SpecFor returns the spec for a key (ok=false for unknown keys).
func SpecFor(k AthleteMetricKey) (AthleteMetricSpec, bool) {
	for _, s := range AthleteMetricSpecs {
		if s.Key == k {
			return s, true
		}
	}
	return AthleteMetricSpec{}, false
}

// ValidAthleteMetricKey reports whether k is a known metric.
func ValidAthleteMetricKey(k AthleteMetricKey) bool {
	_, ok := SpecFor(k)
	return ok
}

// AthleteMetricEntry is one dated measurement of one metric for one user.
// EffectiveDate is a calendar day (time-of-day ignored / stored as UTC midnight).
type AthleteMetricEntry struct {
	ID            AthleteMetricID
	UserID        UserID
	Key           AthleteMetricKey
	EffectiveDate time.Time
	Value         float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Validate enforces the metric's bounds and a positive value.
func (e AthleteMetricEntry) Validate() error {
	spec, ok := SpecFor(e.Key)
	if !ok {
		return fmt.Errorf("%w: unknown athlete metric %q", ErrInvalidAthleteMetric, e.Key)
	}
	if e.Value < spec.Min || e.Value > spec.Max {
		return fmt.Errorf("%w: %s %.1f out of range [%.0f, %.0f] %s",
			ErrInvalidAthleteMetric, spec.Label, e.Value, spec.Min, spec.Max, spec.Unit)
	}
	if e.EffectiveDate.IsZero() {
		return fmt.Errorf("%w: effective_date required", ErrInvalidAthleteMetric)
	}
	return nil
}

// AthleteProfile is a user's full set of metric time series, indexed by key
// and sorted ascending by date. Build it once (NewAthleteProfile) and query
// it with ValueAt — cheap for the per-activity recompute loop.
type AthleteProfile struct {
	byKey map[AthleteMetricKey][]AthleteMetricEntry
}

// NewAthleteProfile groups entries by key and sorts each series by date.
func NewAthleteProfile(entries []AthleteMetricEntry) *AthleteProfile {
	byKey := make(map[AthleteMetricKey][]AthleteMetricEntry)
	for _, e := range entries {
		byKey[e.Key] = append(byKey[e.Key], e)
	}
	for k := range byKey {
		s := byKey[k]
		sort.Slice(s, func(i, j int) bool { return s[i].EffectiveDate.Before(s[j].EffectiveDate) })
		byKey[k] = s
	}
	return &AthleteProfile{byKey: byKey}
}

// ValueAt resolves a metric's value as of date d:
//   - exact/inside the measured range → linear interpolation between the two
//     bracketing entries (by day count),
//   - before the first / after the last entry → clamp to that endpoint,
//   - no entries for the key → (0, false).
func (p *AthleteProfile) ValueAt(k AthleteMetricKey, d time.Time) (float64, bool) {
	s := p.byKey[k]
	if len(s) == 0 {
		return 0, false
	}
	day := truncateUTCDay(d)
	// Clamp below / above the measured range.
	if !day.After(s[0].EffectiveDate) {
		return s[0].Value, true
	}
	last := s[len(s)-1]
	if !day.Before(last.EffectiveDate) {
		return last.Value, true
	}
	// Find the bracketing pair [lo, hi] with lo.date <= day < hi.date.
	for i := 1; i < len(s); i++ {
		hi := s[i]
		if day.Before(hi.EffectiveDate) {
			lo := s[i-1]
			spanDays := hi.EffectiveDate.Sub(lo.EffectiveDate).Hours() / 24
			if spanDays <= 0 {
				return hi.Value, true
			}
			t := (day.Sub(lo.EffectiveDate).Hours() / 24) / spanDays
			return lo.Value + (hi.Value-lo.Value)*t, true
		}
	}
	return last.Value, true
}

// truncateUTCDay drops the time-of-day, returning UTC midnight of t's day. The
// athlete time series is day-granular, so all comparisons normalise to this.
func truncateUTCDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// AthleteThresholds bundles the values EstimateTSS needs, resolved at a date.
// Zero fields mean "no data — use the engine default".
type AthleteThresholds struct {
	FTPWatts       float64
	ThresholdHRBpm float64
}

// ThresholdsAt resolves the TSS-relevant values as of date d. Missing metrics
// stay zero so EstimateTSS falls back to its defaults.
func (p *AthleteProfile) ThresholdsAt(d time.Time) AthleteThresholds {
	var th AthleteThresholds
	if v, ok := p.ValueAt(AthleteFTPWatts, d); ok {
		th.FTPWatts = v
	}
	if v, ok := p.ValueAt(AthleteThresholdHR, d); ok {
		th.ThresholdHRBpm = v
	}
	return th
}
