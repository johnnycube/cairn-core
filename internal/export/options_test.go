package export

import (
	"strings"
	"testing"
)

func TestOptionsFromExclude(t *testing.T) {
	if o, err := OptionsFromExclude(""); err != nil || o != AllOptions() {
		t.Errorf("empty exclude: got %+v, err %v", o, err)
	}
	o, err := OptionsFromExclude("heart_rate, gps,title")
	if err != nil {
		t.Fatalf("exclude parse: %v", err)
	}
	if o.HeartRate || o.GPS || o.Title {
		t.Errorf("excluded features still enabled: %+v", o)
	}
	if !o.Power || !o.Altitude || !o.Distance {
		t.Errorf("non-excluded features disabled: %+v", o)
	}
	if _, err := OptionsFromExclude("hr"); err == nil {
		t.Error("unknown token accepted")
	}
}

func TestApply_StripsExcludedFeatures(t *testing.T) {
	a, s := sampleActivity()
	avgHR := 121
	a.Summary.AvgHeartRateBpm = &avgHR
	opts := AllOptions()
	opts.HeartRate = false
	opts.GPS = false
	opts.Title = false

	fa, fs := Apply(opts, a, s)

	gpx := string(GPX(fa, fs))
	if strings.Contains(gpx, "<trkpt") {
		t.Error("GPX still has trkpt after GPS exclusion")
	}
	tcx := string(TCX(fa, fs))
	for _, gone := range []string{"HeartRateBpm", "LatitudeDegrees", "Morning Ride"} {
		if strings.Contains(tcx, gone) {
			t.Errorf("TCX still contains %q after exclusion", gone)
		}
	}
	for _, kept := range []string{"<ns3:Watts>200</ns3:Watts>", "<Cadence>85</Cadence>", "<AltitudeMeters>100</AltitudeMeters>"} {
		if !strings.Contains(tcx, kept) {
			t.Errorf("TCX lost non-excluded %q", kept)
		}
	}

	// The input must be untouched (Apply works on copies).
	if s.Samples[0].HeartRateBpm == nil || s.Samples[0].Latitude == nil {
		t.Error("Apply mutated the input stream")
	}
	if a.Title != "Morning Ride" || a.Summary.AvgHeartRateBpm == nil {
		t.Error("Apply mutated the input activity")
	}
}

func TestAvailable(t *testing.T) {
	a, s := sampleActivity()
	av := Available(a, s)
	for _, want := range []string{"gps", "altitude", "heart_rate", "power", "cadence", "title"} {
		if !av[want] {
			t.Errorf("expected %q available", want)
		}
	}
	for _, missing := range []string{"distance", "speed", "temperature"} {
		if av[missing] {
			t.Errorf("expected %q unavailable", missing)
		}
	}
}
