package domain

import "math"

// ---------------------------------------------------------------------------
// Segment matching
//
// The matching pipeline runs in two passes:
//
//	1. Spatial candidate filter  — runs in SegmentRepo, returns segments
//	   whose bbox intersects the activity's bbox.
//
//	2. Stream corridor walk      — runs here, in pure Go, per candidate.
//	   Walks the stream sample-by-sample, anchors when a sample is close
//	   to the segment's first polyline vertex, follows along, and emits
//	   a SegmentMatch when the last vertex is reached within tolerance.
//
// The corridor walk is deliberately conservative: it requires the stream
// to stay near the polyline AND to monotonically advance along it. Long
// drifts abort the candidate match; the algorithm then keeps scanning for
// the next anchor.
//
// Multiple matches per (stream, segment) are possible (a hill-repeat
// workout); each becomes a separate SegmentEffort.
// ---------------------------------------------------------------------------

// SegmentMatch is the output of MatchSegment for one traversal. Indices
// are into the supplied samples slice (zero-based, inclusive).
type SegmentMatch struct {
	StartOffset int
	EndOffset   int
	// ElapsedSeconds is samples[end].Timestamp - samples[start].Timestamp,
	// in seconds. Moving-time, gaps, and pause-detection are caller
	// concerns and handled in the use case.
	ElapsedSeconds float64
}

// maxConsecutiveDrift caps the off-corridor streak we tolerate before
// aborting a candidate match. A few off-corridor samples in a row are
// normal (GPS sensor noise, brief tunnel); a sustained drift means the
// rider has left the segment.
const maxConsecutiveDrift = 5

// MatchSegment returns every traversal of the segment found in the stream.
//
// Returns an empty slice (not nil), never errors — malformed inputs
// (too few samples or vertices, missing GPS throughout) simply produce
// no matches.
func MatchSegment(
	samples []StreamSample,
	polyline []GeoPoint,
	tol MatchTolerances,
) []SegmentMatch {
	out := []SegmentMatch{}
	if len(samples) < 2 || len(polyline) < 2 {
		return out
	}

	i := 0
	for i < len(samples) {
		anchor := findAnchor(samples, i, polyline[0], tol.StartToleranceM)
		if anchor < 0 {
			break
		}
		if match := followCorridor(samples, anchor, polyline, tol); match != nil {
			out = append(out, *match)
			// Skip past the match before looking for the next anchor —
			// the same stream cannot contribute two overlapping efforts.
			i = match.EndOffset + 1
		} else {
			i = anchor + 1
		}
	}
	return out
}

// findAnchor returns the index of the first GPS-bearing sample at or
// after `start` whose distance to `target` is within `tolM`. Returns -1
// when no such sample exists.
func findAnchor(samples []StreamSample, start int, target GeoPoint, tolM float64) int {
	for i := start; i < len(samples); i++ {
		p, ok := samplePoint(samples[i])
		if !ok {
			continue
		}
		if HaversineMeters(p, target) <= tolM {
			return i
		}
	}
	return -1
}

// followCorridor walks samples forward from `startIdx` (which is at
// polyline[0]) and returns a SegmentMatch when the last polyline vertex
// is reached within EndToleranceM, or nil if the stream drifts off
// before then.
//
// Advance rule: when the current sample is closer to polyline[polyIdx]
// (the NEXT vertex) than to polyline[polyIdx-1] (the previous vertex),
// the rider has passed the perpendicular bisector and polyIdx advances.
func followCorridor(
	samples []StreamSample,
	startIdx int,
	polyline []GeoPoint,
	tol MatchTolerances,
) *SegmentMatch {
	polyIdx := 1
	last := len(polyline) - 1
	drift := 0

	for j := startIdx + 1; j < len(samples); j++ {
		sp, ok := samplePoint(samples[j])
		if !ok {
			continue
		}

		dPrev := HaversineMeters(sp, polyline[polyIdx-1])
		dNext := HaversineMeters(sp, polyline[polyIdx])

		// Corridor check: perpendicular distance to the current edge, not
		// to its vertices — a mid-edge sample on a long straight edge is
		// exactly on the segment while being far from both endpoints.
		// Vertex distance alone would count it as drift and abort matches
		// on sparse polylines (hand-drawn or simplified segments).
		if distanceToEdgeMeters(sp, polyline[polyIdx-1], polyline[polyIdx]) > tol.CorridorM {
			drift++
			if drift > maxConsecutiveDrift {
				return nil
			}
			continue
		}
		drift = 0

		// Reached final vertex?
		if polyIdx == last && dNext <= tol.EndToleranceM {
			return &SegmentMatch{
				StartOffset:    startIdx,
				EndOffset:      j,
				ElapsedSeconds: samples[j].Timestamp.Sub(samples[startIdx].Timestamp).Seconds(),
			}
		}

		// Advance polyIdx when the sample is closer to the next vertex
		// than to the previous one (passed the bisector).
		if dNext < dPrev && polyIdx < last {
			polyIdx++
		}
	}

	return nil
}

// distanceToEdgeMeters is the shortest distance from p to the edge a→b.
// Uses an equirectangular projection around the edge — exact enough at
// segment-edge scale (tens to hundreds of meters), where the flat-earth
// error is far below GPS noise.
func distanceToEdgeMeters(p, a, b GeoPoint) float64 {
	const earthRadiusM = 6371000.0
	latRad := a.Lat * math.Pi / 180
	mPerDegLat := earthRadiusM * math.Pi / 180
	mPerDegLon := mPerDegLat * math.Cos(latRad)

	ax, ay := 0.0, 0.0
	bx := (b.Lon - a.Lon) * mPerDegLon
	by := (b.Lat - a.Lat) * mPerDegLat
	px := (p.Lon - a.Lon) * mPerDegLon
	py := (p.Lat - a.Lat) * mPerDegLat

	dx, dy := bx-ax, by-ay
	segLenSq := dx*dx + dy*dy
	if segLenSq == 0 {
		return math.Hypot(px, py) // degenerate edge: distance to the point
	}
	// Project p onto the edge, clamped to [0,1] so off-end points measure
	// to the nearest endpoint.
	t := (px*dx + py*dy) / segLenSq
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-t*dx, py-t*dy)
}

// samplePoint returns the sample's GPS point and ok=true when both
// latitude and longitude are set. Samples without a GPS fix are skipped
// by the matching algorithm.
func samplePoint(s StreamSample) (GeoPoint, bool) {
	if s.Latitude == nil || s.Longitude == nil {
		return GeoPoint{}, false
	}
	return GeoPoint{Lat: *s.Latitude, Lon: *s.Longitude}, true
}

// ---------------------------------------------------------------------------
// Bounding box
// ---------------------------------------------------------------------------

// SampleBoundingBox computes the min/max lat&lon over a stream's samples.
// Returns ok=false when no sample carries GPS. Used by the matching use
// case to build the spatial filter passed to SegmentRepo.
func SampleBoundingBox(samples []StreamSample) (minLat, maxLat, minLon, maxLon float64, ok bool) {
	first := true
	for _, s := range samples {
		p, has := samplePoint(s)
		if !has {
			continue
		}
		if first {
			minLat, maxLat = p.Lat, p.Lat
			minLon, maxLon = p.Lon, p.Lon
			first = false
			continue
		}
		if p.Lat < minLat {
			minLat = p.Lat
		}
		if p.Lat > maxLat {
			maxLat = p.Lat
		}
		if p.Lon < minLon {
			minLon = p.Lon
		}
		if p.Lon > maxLon {
			maxLon = p.Lon
		}
	}
	return minLat, maxLat, minLon, maxLon, !first
}

// ExpandBoundingBox grows the box by approximately `meters` on every side.
//
// Uses a flat-earth approximation: 1° latitude ≈ 111 km; 1° longitude at
// the box's mean latitude ≈ 111 km · cos(lat). Good enough for the
// candidate-filter use case where we want to include nearby segments.
func ExpandBoundingBox(minLat, maxLat, minLon, maxLon, meters float64) (float64, float64, float64, float64) {
	const metersPerDegreeLat = 111_000.0
	dLat := meters / metersPerDegreeLat
	midLat := (minLat + maxLat) / 2
	metersPerDegreeLon := metersPerDegreeLat * math.Cos(midLat*math.Pi/180)
	if metersPerDegreeLon < 1 {
		metersPerDegreeLon = 1 // avoid blow-up near poles
	}
	dLon := meters / metersPerDegreeLon
	return minLat - dLat, maxLat + dLat, minLon - dLon, maxLon + dLon
}
