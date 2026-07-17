package domain

import (
	"math"
	"testing"
	"time"
)

// mkSamples builds a GPS stream along the given points, one sample per
// second. Points are (lat, lon) pairs.
func mkSamples(points []GeoPoint) []StreamSample {
	t0 := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	out := make([]StreamSample, len(points))
	for i, p := range points {
		lat, lon := p.Lat, p.Lon
		out[i] = StreamSample{
			Timestamp: t0.Add(time.Duration(i) * time.Second),
			Latitude:  &lat,
			Longitude: &lon,
		}
	}
	return out
}

// northTrack returns n points heading due north from (lat, lon), stepM
// meters apart. 1° latitude ≈ 111,320 m.
func northTrack(lat, lon float64, n int, stepM float64) []GeoPoint {
	const mPerDegLat = 111320.0
	out := make([]GeoPoint, n)
	for i := range out {
		out[i] = GeoPoint{Lat: lat + float64(i)*stepM/mPerDegLat, Lon: lon}
	}
	return out
}

func TestMatchSegment_DensePolyline(t *testing.T) {
	// 500 m straight segment with a vertex every 25 m; stream samples every 20 m.
	poly := northTrack(47.0, 8.0, 21, 25)
	samples := mkSamples(northTrack(47.0, 8.0, 26, 20))

	matches := MatchSegment(samples, poly, DefaultMatchTolerances())
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].StartOffset != 0 || matches[0].EndOffset != 25 {
		t.Errorf("match window = [%d,%d], want [0,25]", matches[0].StartOffset, matches[0].EndOffset)
	}
}

func TestMatchSegment_SparsePolyline_MidEdgeSamplesStayInCorridor(t *testing.T) {
	// The segment is two vertices 500 m apart — every mid-edge sample is
	// >200 m from both vertices but exactly ON the segment. Vertex-distance
	// corridor logic would abort this; edge-distance must match it.
	poly := []GeoPoint{{Lat: 47.0, Lon: 8.0}, {Lat: 47.0 + 500.0/111320.0, Lon: 8.0}}
	samples := mkSamples(northTrack(47.0, 8.0, 26, 20))

	matches := MatchSegment(samples, poly, DefaultMatchTolerances())
	if len(matches) != 1 {
		t.Fatalf("sparse polyline: matches = %d, want 1", len(matches))
	}
}

func TestMatchSegment_VeersOffCorridor_NoMatch(t *testing.T) {
	// Stream starts on the segment but veers 90° east after 100 m; the
	// sustained off-corridor drift must abort the match.
	poly := northTrack(47.0, 8.0, 21, 25)
	const mPerDegLat = 111320.0
	mPerDegLon := mPerDegLat * math.Cos(47.0*math.Pi/180)

	pts := northTrack(47.0, 8.0, 6, 20) // first 100 m on-segment
	last := pts[len(pts)-1]
	for i := 1; i <= 20; i++ { // then due east, 20 m steps
		pts = append(pts, GeoPoint{Lat: last.Lat, Lon: last.Lon + float64(i)*20/mPerDegLon})
	}
	matches := MatchSegment(mkSamples(pts), poly, DefaultMatchTolerances())
	if len(matches) != 0 {
		t.Fatalf("veering stream: matches = %d, want 0", len(matches))
	}
}

func TestMatchSegment_StopsShortOfEnd_NoMatch(t *testing.T) {
	// Stream covers only the first 300 m of a 500 m segment — never reaches
	// the end vertex, so no effort may be emitted.
	poly := northTrack(47.0, 8.0, 21, 25)
	samples := mkSamples(northTrack(47.0, 8.0, 16, 20)) // 300 m

	matches := MatchSegment(samples, poly, DefaultMatchTolerances())
	if len(matches) != 0 {
		t.Fatalf("partial coverage: matches = %d, want 0", len(matches))
	}
}

func TestMatchSegment_RepeatTraversals(t *testing.T) {
	// Hill repeats: ride the segment, loop back far off-corridor, ride it
	// again → two distinct efforts.
	poly := northTrack(47.0, 8.0, 21, 25)
	const mPerDegLat = 111320.0
	mPerDegLon := mPerDegLat * math.Cos(47.0*math.Pi/180)

	up := northTrack(47.0, 8.0, 26, 20)
	var back []GeoPoint
	for i := 25; i >= 0; i-- { // return leg 100 m east of the segment
		back = append(back, GeoPoint{Lat: up[i].Lat, Lon: up[i].Lon + 100/mPerDegLon})
	}
	pts := append(append(append([]GeoPoint{}, up...), back...), up...)

	matches := MatchSegment(mkSamples(pts), poly, DefaultMatchTolerances())
	if len(matches) != 2 {
		t.Fatalf("repeats: matches = %d, want 2", len(matches))
	}
}

func TestDistanceToEdgeMeters(t *testing.T) {
	a := GeoPoint{Lat: 47.0, Lon: 8.0}
	b := GeoPoint{Lat: 47.0 + 1000.0/111320.0, Lon: 8.0} // 1 km due north

	// Mid-edge, 10 m east of the line.
	mPerDegLon := 111320.0 * math.Cos(47.0*math.Pi/180)
	p := GeoPoint{Lat: 47.0 + 500.0/111320.0, Lon: 8.0 + 10.0/mPerDegLon}
	if d := distanceToEdgeMeters(p, a, b); math.Abs(d-10) > 1 {
		t.Errorf("mid-edge distance = %.2f, want ≈10", d)
	}

	// Beyond the far end: distance clamps to endpoint b.
	p = GeoPoint{Lat: 47.0 + 1100.0/111320.0, Lon: 8.0}
	if d := distanceToEdgeMeters(p, a, b); math.Abs(d-100) > 2 {
		t.Errorf("off-end distance = %.2f, want ≈100", d)
	}

	// Degenerate edge (a == b): plain point distance.
	if d := distanceToEdgeMeters(p, a, a); d < 1000 {
		t.Errorf("degenerate edge distance = %.2f, want >1000", d)
	}
}
