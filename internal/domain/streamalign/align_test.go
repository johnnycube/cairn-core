package streamalign

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

var t0 = time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }

func fptr(v float64) *float64 { return &v }
func i16(v int16) *int16      { return &v }

// build a stream from per-second altitude + hr values starting at offset.
func mkStream(id string, offset time.Duration, step time.Duration, alt []float64, hr []int16) domain.Stream {
	n := len(alt)
	if len(hr) > n {
		n = len(hr)
	}
	samples := make([]domain.StreamSample, n)
	for i := 0; i < n; i++ {
		s := domain.StreamSample{Timestamp: t0.Add(offset + time.Duration(i)*step)}
		if i < len(alt) {
			s.AltitudeM = fptr(alt[i])
		}
		if i < len(hr) {
			s.HeartRateBpm = i16(hr[i])
		}
		samples[i] = s
	}
	return domain.Stream{ActivitySourceID: domain.SourceID(mustUUID(id)), Samples: samples}
}

func TestBuild_PerChannelFromDifferentSources(t *testing.T) {
	// Garmin has altitude (barometric), Strava has heart rate. Per-channel merge
	// should take altitude from garmin and HR from strava.
	garmin := mkStream("11111111-1111-1111-1111-111111111111", 0, time.Second,
		[]float64{100, 101, 102, 103, 104}, nil)
	strava := mkStream("22222222-2222-2222-2222-222222222222", 0, time.Second,
		nil, []int16{120, 122, 124, 126, 128})

	streams := map[domain.SourceID]domain.Stream{
		garmin.ActivitySourceID: garmin,
		strava.ActivitySourceID: strava,
	}
	merged := Build(streams, nil, Options{})

	if len(merged.Grid) == 0 {
		t.Fatal("empty grid")
	}
	if merged.Provenance[domain.StreamChannelAltitude] != garmin.ActivitySourceID {
		t.Errorf("altitude should come from garmin")
	}
	if merged.Provenance[domain.StreamChannelHeartRate] != strava.ActivitySourceID {
		t.Errorf("hr should come from strava")
	}
	alt := merged.Channels[domain.StreamChannelAltitude]
	if alt == nil || alt[0] == nil || *alt[0] != 100 {
		t.Errorf("altitude[0] = %v, want 100", deref(alt[0]))
	}
}

func TestResample_GapCapLeavesMissing(t *testing.T) {
	// Two points 5 minutes apart with a 30s gap cap → interior grid points nil.
	pts := []point{{t0, 100}, {t0.Add(5 * time.Minute), 200}}
	grid := buildGrid(t0, t0.Add(5*time.Minute), time.Minute)
	out := resample(pts, grid, 30*time.Second)
	if out[0] == nil || *out[0] != 100 {
		t.Errorf("endpoint should be present")
	}
	for i := 1; i < len(out)-1; i++ {
		if out[i] != nil {
			t.Errorf("grid[%d] should be nil (gap > cap), got %v", i, *out[i])
		}
	}
}

func TestResample_LinearInterpolation(t *testing.T) {
	pts := []point{{t0, 0}, {t0.Add(10 * time.Second), 100}}
	grid := []time.Time{t0.Add(5 * time.Second)}
	out := resample(pts, grid, time.Minute)
	if out[0] == nil || *out[0] != 50 {
		t.Errorf("midpoint interp = %v, want 50", deref(out[0]))
	}
}

func TestBuild_ClockOffsetAlignment(t *testing.T) {
	// Same activity, but strava's clock is 3s behind garmin (constant skew).
	// With AlignStarts, the two altitude tracks should align (same values per
	// grid point), not be shifted apart.
	garmin := mkStream("11111111-1111-1111-1111-111111111111", 0, time.Second,
		[]float64{100, 110, 120, 130, 140}, nil)
	strava := mkStream("22222222-2222-2222-2222-222222222222", 3*time.Second, time.Second,
		[]float64{100, 110, 120, 130, 140}, nil)
	streams := map[domain.SourceID]domain.Stream{
		garmin.ActivitySourceID: garmin,
		strava.ActivitySourceID: strava,
	}
	// Force altitude winner = strava to test its shifted timeline.
	merged := Build(streams, map[domain.StreamChannel]domain.SourceID{
		domain.StreamChannelAltitude: strava.ActivitySourceID,
	}, Options{AlignStarts: true})
	if merged.Channels[domain.StreamChannelAltitude][0] == nil ||
		*merged.Channels[domain.StreamChannelAltitude][0] != 100 {
		t.Errorf("aligned strava altitude[0] = %v, want 100 (start-aligned)",
			deref(merged.Channels[domain.StreamChannelAltitude][0]))
	}
}

func TestMedianInterval(t *testing.T) {
	s := []domain.StreamSample{
		{Timestamp: t0}, {Timestamp: t0.Add(time.Second)},
		{Timestamp: t0.Add(2 * time.Second)}, {Timestamp: t0.Add(time.Minute)}, // one big gap
	}
	if got := medianInterval(s); got != time.Second {
		t.Errorf("median = %v, want 1s (robust to the outlier gap)", got)
	}
}

func deref(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
