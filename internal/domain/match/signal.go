package match

import (
	"math"
	"strings"
	"time"
)

// Similarity signals: each returns [0,1] via a decay curve. All times UTC.

// startTimeTau: decay constant for exp(-|Δt|/tau) (~0.96 at 5s, ~0.08 at 5min).
const startTimeTau = 120 * time.Second

func startTimeSimilarity(a, b time.Time) float64 {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return math.Exp(-float64(d) / float64(startTimeTau))
}

// relativeCap: relative difference at which scalar similarity hits 0.
const relativeCap = 0.15

// relativeSimilarity scores two positive scalars by relative difference.
// Returns (0,false) when either is non-positive so the caller drops the term.
func relativeSimilarity(a, b float64) (float64, bool) {
	if a <= 0 || b <= 0 {
		return 0, false
	}
	rel := math.Abs(a-b) / math.Max(a, b)
	s := 1 - rel/relativeCap
	if s < 0 {
		s = 0
	}
	return s, true
}

// Sport class is a gate, matched fuzzily via a synonym→group map. Blank/"workout"
// and other generic classes are wildcards compatible with anything.

var sportGroups = map[string]string{
	"run": "run", "running": "run", "trailrun": "run", "trailrunning": "run",
	"treadmill": "run", "virtualrun": "run", "track": "run",
	"ride": "ride", "cycling": "ride", "virtualride": "ride", "ebikeride": "ride",
	"gravelride": "ride", "mountainbikeride": "ride", "mtb": "ride", "handcycle": "ride",
	"swim": "swim", "swimming": "swim", "openwaterswim": "swim", "lapswim": "swim",
	"poolswim": "swim",
	"walk": "walk", "walking": "walk", "hike": "walk", "hiking": "walk",
	"row": "row", "rowing": "row", "kayaking": "row", "canoeing": "row",
	"nordicski": "ski", "alpineski": "ski", "backcountryski": "ski", "snowboard": "ski",
	"rollerski": "ski",
}

var wildcardSports = map[string]struct{}{
	"": {}, "workout": {}, "other": {}, "elliptical": {}, "weighttraining": {},
	"crossfit": {}, "yoga": {},
}

func normalizeSport(s string) string {
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// sportGroup maps a class onto its canonical group; unknown classes group by
// their normalized name so identical unknowns still match.
func sportGroup(s string) (group string, wildcard bool) {
	n := normalizeSport(s)
	if _, ok := wildcardSports[n]; ok {
		return "", true
	}
	if g, ok := sportGroups[n]; ok {
		return g, false
	}
	return n, false
}

// sportsCompatible reports whether two classes could be the same activity.
func sportsCompatible(a, b string) bool {
	ga, wa := sportGroup(a)
	gb, wb := sportGroup(b)
	return wa || wb || ga == gb
}

// haversineMeters is the great-circle distance between two lat/lng points.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}
