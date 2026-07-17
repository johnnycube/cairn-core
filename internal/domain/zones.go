package domain

// Training zones — time spent in each heart-rate / power band.
//
// Zones are defined as fractions of a personal reference value (LTHR, max HR,
// or FTP) resolved from the athlete profile at the activity's date. Bucketing
// is time-weighted: each stream sample contributes the seconds until the next
// sample (clamped, so pauses don't inflate a zone).

// ZoneBand is one zone, expressed as a fraction of the reference value.
// HighPct == 0 means the band is open-ended at the top.
type ZoneBand struct {
	Label   string
	LowPct  float64
	HighPct float64
}

// Heart-rate zones as fractions of lactate-threshold HR (Friel 5-zone model).
var hrZonesLTHR = []ZoneBand{
	{"Z1 Recovery", 0.00, 0.81},
	{"Z2 Aerobic", 0.81, 0.90},
	{"Z3 Tempo", 0.90, 0.94},
	{"Z4 Threshold", 0.94, 1.00},
	{"Z5 VO₂max", 1.00, 0},
}

// Heart-rate zones as fractions of max HR (used when no LTHR is set).
var hrZonesMax = []ZoneBand{
	{"Z1 Recovery", 0.00, 0.60},
	{"Z2 Aerobic", 0.60, 0.70},
	{"Z3 Tempo", 0.70, 0.80},
	{"Z4 Threshold", 0.80, 0.90},
	{"Z5 Max", 0.90, 0},
}

// Power zones as fractions of FTP (Coggan 7-zone model).
var powerZonesFTP = []ZoneBand{
	{"Z1 Recovery", 0.00, 0.55},
	{"Z2 Endurance", 0.55, 0.75},
	{"Z3 Tempo", 0.75, 0.90},
	{"Z4 Threshold", 0.90, 1.05},
	{"Z5 VO₂max", 1.05, 1.20},
	{"Z6 Anaerobic", 1.20, 1.50},
	{"Z7 Neuromuscular", 1.50, 0},
}

// HRZoneBands returns the HR zone model for the basis ("lthr" or "max").
func HRZoneBands(basis string) []ZoneBand {
	if basis == "max" {
		return hrZonesMax
	}
	return hrZonesLTHR
}

// PowerZoneBands returns the FTP-relative power zone model.
func PowerZoneBands() []ZoneBand { return powerZonesFTP }

// AerobicDecoupling computes the cardiac drift of an effort: the percentage
// rise in heart-rate cost per unit of output (power for rides, speed for runs)
// from the first half to the second half. hr and output are parallel, time-
// ordered sample slices (only positive-on-both samples should be passed). A low
// value (<5%) means the effort was "coupled" — a marker of aerobic fitness;
// a high value means HR drifted up relative to output (fatigue, heat, or a
// pace/power that's above the athlete's aerobic threshold).
//
//	decoupling = (EF_firstHalf − EF_secondHalf) / EF_firstHalf × 100,
//	where EF (efficiency factor) = mean(output) / mean(hr) over the half.
//
// Returns (pct, ok); ok is false when there isn't enough data to split.
func AerobicDecoupling(hr, output []float64) (float64, bool) {
	n := len(hr)
	if n < 20 || len(output) != n {
		return 0, false
	}
	mid := n / 2
	ef := func(h, o []float64) (float64, bool) {
		var sh, so float64
		for i := range h {
			sh += h[i]
			so += o[i]
		}
		if sh <= 0 {
			return 0, false
		}
		meanHR := sh / float64(len(h))
		meanOut := so / float64(len(o))
		if meanHR <= 0 {
			return 0, false
		}
		return meanOut / meanHR, true
	}
	ef1, ok1 := ef(hr[:mid], output[:mid])
	ef2, ok2 := ef(hr[mid:], output[mid:])
	if !ok1 || !ok2 || ef1 == 0 {
		return 0, false
	}
	return (ef1 - ef2) / ef1 * 100, true
}

// BucketSeconds assigns each (value, dtSeconds) pair to a zone band by
// value/reference and returns the total seconds in each band (same order as
// bands). vals and dts must be equal length; a value lands in the highest band
// whose LowPct it reaches.
func BucketSeconds(bands []ZoneBand, reference float64, vals, dts []float64) []float64 {
	out := make([]float64, len(bands))
	if reference <= 0 || len(bands) == 0 {
		return out
	}
	for i, v := range vals {
		if v <= 0 {
			continue
		}
		ratio := v / reference
		idx := 0
		for b := range bands {
			if ratio >= bands[b].LowPct {
				idx = b
			}
		}
		out[idx] += dts[i]
	}
	return out
}
