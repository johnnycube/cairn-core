package domain

import "time"

// Training-stress estimation.
//
// TSS (Training Stress Score) quantifies a single workout's training load on a
// scale where one hour at threshold intensity = 100. The training-load curves
// (CTL/ATL/TSB) are EWMAs over daily TSS, so without a per-activity TSS the
// whole Analysis view is flat.
//
// Providers rarely hand us a ready-made TSS (Strava never does), so we estimate
// it from whatever intensity signal the merged summary carries. The estimate is
// deliberately simple and self-contained; per-user FTP / threshold-HR / pace
// thresholds are a v2 refinement that will replace the defaults below without
// changing call sites.

// Default thresholds used when the user has not configured personal values.
const (
	defaultFTPWatts       = 220.0 // functional threshold power (cycling)
	defaultThresholdHRBpm = 168.0 // lactate-threshold heart rate
	defaultIntensity      = 0.70  // assumed IF when no power/HR signal exists
)

// EstimateTSS derives a Training Stress Score from an activity's summary metrics
// and moving duration, using the best available intensity signal:
//
//  1. Power:  IF = NormalizedPower / FTP        (preferred — cycling)
//  2. Heart:  IF = avgHR / thresholdHR          (fallback — most other sports)
//  3. Neither: a generic default intensity
//
// FTP and threshold HR come from the athlete's profile, resolved at the
// activity's date (th); zero fields fall back to the engine defaults so a user
// who hasn't entered their values still gets a usable estimate.
//
// TSS = movingHours × IF² × 100, so one hour at threshold yields 100. The
// intensity factor is clamped to a sane [0.3, 1.15] band so a single noisy
// field can't produce an absurd load. Returns (tss, intensityFactor, ok); ok is
// false when there isn't enough data (no moving time) to estimate anything.
func EstimateTSS(s ActivitySummary, moving time.Duration, th AthleteThresholds) (tss, intensityFactor float64, ok bool) {
	hours := moving.Hours()
	if hours <= 0 {
		return 0, 0, false
	}
	if_ := estimateIntensityFactor(s, th)
	return hours * if_ * if_ * 100, if_, true
}

func estimateIntensityFactor(s ActivitySummary, th AthleteThresholds) float64 {
	ftp := th.FTPWatts
	if ftp <= 0 {
		ftp = defaultFTPWatts
	}
	lthr := th.ThresholdHRBpm
	if lthr <= 0 {
		lthr = defaultThresholdHRBpm
	}
	var if_ float64
	switch {
	case s.NormalizedPowerW != nil && *s.NormalizedPowerW > 0:
		if_ = float64(*s.NormalizedPowerW) / ftp
	case s.AvgPowerW != nil && *s.AvgPowerW > 0:
		// No normalized power: approximate NP ≈ 1.05 × average for the
		// variability of real-world efforts.
		if_ = float64(*s.AvgPowerW) * 1.05 / ftp
	case s.AvgHeartRateBpm != nil && *s.AvgHeartRateBpm > 0:
		if_ = float64(*s.AvgHeartRateBpm) / lthr
	default:
		if_ = defaultIntensity
	}
	switch {
	case if_ < 0.3:
		return 0.3
	case if_ > 1.15:
		return 1.15
	default:
		return if_
	}
}
