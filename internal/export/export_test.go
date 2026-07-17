package export

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tormoder/fit"

	"github.com/johnnycube/cairn-core/internal/domain"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int16) *int16     { return &v }

func sampleActivity() (domain.Activity, domain.Stream) {
	t0 := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)
	a := domain.Activity{
		Type:            domain.ActivityTypeRide,
		Title:           "Morning Ride",
		StartTime:       t0,
		ElapsedDuration: 2 * time.Second,
		Summary:         domain.ActivitySummary{DistanceM: ptrF(30)},
	}
	s := domain.Stream{Samples: []domain.StreamSample{
		{Timestamp: t0, Latitude: ptrF(49.87), Longitude: ptrF(8.65), AltitudeM: ptrF(100), HeartRateBpm: ptrI(120), PowerW: ptrI(200), Cadence: ptrI(85)},
		{Timestamp: t0.Add(time.Second), Latitude: ptrF(49.871), Longitude: ptrF(8.651), AltitudeM: ptrF(101), HeartRateBpm: ptrI(122)},
		{Timestamp: t0.Add(2 * time.Second), HeartRateBpm: ptrI(125), PowerW: ptrI(210)}, // no GPS (indoor-ish)
	}}
	return a, s
}

func TestGPX_StructureAndExtensions(t *testing.T) {
	a, s := sampleActivity()
	out := string(GPX(a, s))

	for _, want := range []string{
		`<?xml version="1.0"`,
		`<gpx version="1.1" creator="Cairn"`,
		`xmlns:gpxtpx=`,
		`<trkpt lat="49.87" lon="8.65">`,
		`<ele>100</ele>`,
		`<gpxtpx:hr>120</gpxtpx:hr>`,
		`<gpxtpx:cad>85</gpxtpx:cad>`,
		`<power>200</power>`,
		`<name>Morning Ride</name>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("GPX missing %q", want)
		}
	}
	// The no-GPS third sample must NOT produce a trkpt.
	if strings.Count(out, "<trkpt") != 2 {
		t.Errorf("expected 2 trkpt (GPS-only), got %d", strings.Count(out, "<trkpt"))
	}
}

func TestTCX_CarriesAllSamplesIncludingNoGPS(t *testing.T) {
	a, s := sampleActivity()
	out := string(TCX(a, s))

	for _, want := range []string{
		`<TrainingCenterDatabase`,
		`Sport="Biking"`,
		`<HeartRateBpm><Value>120</Value></HeartRateBpm>`,
		`<ns3:Watts>200</ns3:Watts>`,
		`<Cadence>85</Cadence>`,
		`<LatitudeDegrees>49.87</LatitudeDegrees>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TCX missing %q", want)
		}
	}
	// All three samples are trackpoints in TCX (no-GPS included).
	if strings.Count(out, "<Trackpoint>") != 3 {
		t.Errorf("expected 3 Trackpoints, got %d", strings.Count(out, "<Trackpoint>"))
	}
}

func TestFilename(t *testing.T) {
	a, _ := sampleActivity()
	if got := Filename(a, "gpx"); got != "2026-06-05-morning-ride.gpx" {
		t.Errorf("Filename = %q", got)
	}
}

func TestEscaping(t *testing.T) {
	a, s := sampleActivity()
	a.Title = `A & B <"x">`
	out := string(GPX(a, s))
	if strings.Contains(out, `A & B <"x">`) {
		t.Error("title not XML-escaped in GPX")
	}
	if !strings.Contains(out, "A &amp; B &lt;") {
		t.Error("expected escaped title in GPX")
	}
}

func TestFIT_RoundTrip(t *testing.T) {
	a, s := sampleActivity()
	a.EndTime = a.StartTime.Add(2 * time.Second)
	data, err := FIT(a, s)
	if err != nil {
		t.Fatalf("FIT encode: %v", err)
	}
	if len(data) < 14 {
		t.Fatalf("FIT output too small: %d bytes", len(data))
	}

	// Decode it back with the same library to prove it's a valid FIT file.
	f, err := fit.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode FIT: %v", err)
	}
	if f.FileId.Type != fit.FileTypeActivity {
		t.Fatalf("file type = %v", f.FileId.Type)
	}
	act, err := f.Activity()
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(act.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(act.Records))
	}
	// First record carried lat/lon/alt/hr/power — check they survive (scaled).
	r0 := act.Records[0]
	if got := r0.PositionLat.Degrees(); abs(got-49.87) > 1e-4 {
		t.Errorf("lat round-trip = %v, want ~49.87", got)
	}
	if got := r0.GetAltitudeScaled(); abs(got-100) > 1 {
		t.Errorf("altitude round-trip = %v, want ~100", got)
	}
	if r0.HeartRate != 120 {
		t.Errorf("hr round-trip = %d, want 120", r0.HeartRate)
	}
	if r0.Power != 200 {
		t.Errorf("power round-trip = %d, want 200", r0.Power)
	}
	if len(act.Sessions) != 1 || len(act.Laps) != 1 {
		t.Errorf("expected 1 session + 1 lap, got %d/%d", len(act.Sessions), len(act.Laps))
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
