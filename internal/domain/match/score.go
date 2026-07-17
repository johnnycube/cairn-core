package match

// Weights for the scored signals. Renormalized over whichever signals are
// present, so a distance-less indoor activity is scored on time+duration.
type Weights struct {
	StartTime float64
	Distance  float64
	Duration  float64
}

// DefaultWeights — calibrate against a real corpus before trusting thresholds.
func DefaultWeights() Weights {
	return Weights{StartTime: 0.5, Distance: 0.3, Duration: 0.2}
}

// GPS tiebreaker bounds (start-coordinate separation, meters).
const (
	gpsFarMeters  = 1000.0 // beyond this: almost certainly different, damp hard
	gpsNearMeters = 200.0  // within this: agrees, no penalty
)

// Score returns the [0,1] similarity of two records and whether the pair is
// gated out (incompatible sport). Gated pairs score 0 and must not cluster.
func Score(a, b SourceFeatures, w Weights) (score float64, gated bool) {
	if !sportsCompatible(a.SportClass, b.SportClass) {
		return 0, true
	}

	var sum, wsum float64
	add := func(weight, sig float64) {
		sum += weight * sig
		wsum += weight
	}

	add(w.StartTime, startTimeSimilarity(a.StartUTC, b.StartUTC))
	if s, ok := relativeSimilarity(a.DistanceM, b.DistanceM); ok {
		add(w.Distance, s)
	}
	// Duration: moving-vs-moving, never mixed; fall back to elapsed.
	if s, ok := relativeSimilarity(float64(a.MovingS), float64(b.MovingS)); ok {
		add(w.Duration, s)
	} else if s, ok := relativeSimilarity(float64(a.ElapsedS), float64(b.ElapsedS)); ok {
		add(w.Duration, s)
	}

	if wsum == 0 {
		return 0, false
	}
	score = sum / wsum

	// GPS tiebreaker, only when both have coordinates (absent GPS isn't evidence).
	if a.StartLat != nil && a.StartLng != nil && b.StartLat != nil && b.StartLng != nil {
		d := haversineMeters(*a.StartLat, *a.StartLng, *b.StartLat, *b.StartLng)
		switch {
		case d <= gpsNearMeters:
			score += (1 - score) * 0.05
		case d >= gpsFarMeters:
			score *= 0.25
		default:
			frac := (d - gpsNearMeters) / (gpsFarMeters - gpsNearMeters)
			score *= 1 - 0.75*frac
		}
	}
	return score, false
}
