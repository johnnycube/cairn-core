package besteffort

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

func TestSlidingWindowMax_FindsLastWindow(t *testing.T) {
	// values = [0, 1, 2, ..., 59]. The max 5-sample window is the tail.
	values := make([]float64, 60)
	for i := range values {
		values[i] = float64(i)
	}

	avg, start, ok := slidingWindowMax(values, 5)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if start != 55 {
		t.Errorf("start = %d, want 55", start)
	}
	wantAvg := (55.0 + 56 + 57 + 58 + 59) / 5
	if math.Abs(avg-wantAvg) > 1e-9 {
		t.Errorf("avg = %f, want %f", avg, wantAvg)
	}
}

func TestSlidingWindowMax_BelowWindow(t *testing.T) {
	values := []float64{1, 2, 3}
	_, _, ok := slidingWindowMax(values, 10)
	if ok {
		t.Error("expected ok=false for activity shorter than window")
	}
}

func TestSlidingWindowMax_TolerantToNaN(t *testing.T) {
	// Place high power at 200, surrounded by NaN. 80%-valid threshold
	// should reject a window dominated by NaN.
	values := make([]float64, 30)
	for i := range values {
		values[i] = math.NaN()
	}
	values[10] = 1000

	// 5-sample window with only one valid sample: 20% valid, below 80%
	// threshold — should be ignored.
	_, _, ok := slidingWindowMax(values, 5)
	if ok {
		t.Error("expected ok=false: single valid sample insufficient")
	}

	// Now fill the window properly with 4 valid samples of value 100 each
	// (80% of 5 = 4 valid samples).
	for i := 10; i < 14; i++ {
		values[i] = 100
	}
	avg, _, ok := slidingWindowMax(values, 5)
	if !ok {
		t.Fatal("expected ok=true with 4 valid + 1 NaN in a 5-sample window")
	}
	if math.Abs(avg-100) > 1e-9 {
		t.Errorf("avg = %f, want 100 (NaN excluded from mean)", avg)
	}
}

func TestSlidingWindowMax_PicksBestNotLastOrFirst(t *testing.T) {
	// Spike in the middle.
	values := []float64{50, 50, 50, 50, 50, 50, 50, 200, 200, 200, 50, 50, 50}
	avg, start, ok := slidingWindowMax(values, 3)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if start != 7 {
		t.Errorf("start = %d, want 7", start)
	}
	if math.Abs(avg-200) > 1e-9 {
		t.Errorf("avg = %f, want 200", avg)
	}
}

// ---------------------------------------------------------------------------
// slidingWindowMinPace
// ---------------------------------------------------------------------------

// makeUniformPaceSamples builds N samples spaced 1 second apart, each
// `metresPerStep` ahead of the previous. Result has constant pace of
// (1 / metresPerStep) * 1000 seconds per km.
func makeUniformPaceSamples(n int, metresPerStep float64) []domain.StreamSample {
	out := make([]domain.StreamSample, n)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		dist := float64(i) * metresPerStep
		out[i] = domain.StreamSample{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			DistanceM: &dist,
		}
	}
	return out
}

func TestSlidingWindowMinPace_UniformPace(t *testing.T) {
	// 100 samples, 4 m/s pace (4 m per step, 1 step per second) → pace = 250 s/km.
	samples := makeUniformPaceSamples(100, 4)
	pace, startIdx, endIdx, dist, ok := slidingWindowMinPace(samples, 100)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// 100m at 4 m/s = 25 seconds elapsed. 25s / (100m / 1000) = 250 s/km.
	if math.Abs(pace-250) > 1 {
		t.Errorf("pace = %f s/km, want 250 ± 1", pace)
	}
	if endIdx-startIdx < 25 || endIdx-startIdx > 26 {
		t.Errorf("window length = %d samples, want 25 or 26", endIdx-startIdx)
	}
	if dist < 100 {
		t.Errorf("dist = %f, must be >= 100", dist)
	}
}

func TestSlidingWindowMinPace_BelowDistance(t *testing.T) {
	// Only 50m of distance — can't fit a 100m window.
	samples := makeUniformPaceSamples(50, 1)
	_, _, _, _, ok := slidingWindowMinPace(samples, 100)
	if ok {
		t.Error("expected ok=false: total distance below window")
	}
}

func TestSlidingWindowMinPace_PicksFastestSegment(t *testing.T) {
	// 1km of uniform 4 m/s, then a 200m surge at 8 m/s, then more 4 m/s.
	// The fastest 200m should be entirely in the surge with pace = 125 s/km.
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	var samples []domain.StreamSample
	var dist float64
	ts := base

	// 250s of slow (250 samples × 4 m = 1000 m).
	for i := 0; i < 250; i++ {
		d := dist
		samples = append(samples, domain.StreamSample{Timestamp: ts, DistanceM: &d})
		dist += 4
		ts = ts.Add(time.Second)
	}
	// 25s of fast (25 samples × 8 m = 200 m).
	for i := 0; i < 25; i++ {
		d := dist
		samples = append(samples, domain.StreamSample{Timestamp: ts, DistanceM: &d})
		dist += 8
		ts = ts.Add(time.Second)
	}
	// 250s more slow.
	for i := 0; i < 250; i++ {
		d := dist
		samples = append(samples, domain.StreamSample{Timestamp: ts, DistanceM: &d})
		dist += 4
		ts = ts.Add(time.Second)
	}

	pace, startIdx, _, _, ok := slidingWindowMinPace(samples, 200)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// The fastest 200m window is the surge: 25 seconds for 200m → 125 s/km.
	if math.Abs(pace-125) > 2 {
		t.Errorf("pace = %f s/km, want 125 ± 2", pace)
	}
	// Surge starts at index 250.
	if startIdx < 245 || startIdx > 255 {
		t.Errorf("startIdx = %d, want around 250", startIdx)
	}
}

func TestComputeVAMEffort(t *testing.T) {
	fixedNow := func() time.Time { return time.Unix(0, 0).UTC() }
	fixedID := func() uuid.UUID { return uuid.Nil }
	uc := NewComputeBestEffortsForSource(nil, nil, nil, nil, fixedNow, fixedID)

	// 400 samples at 1Hz, climbing 1 m/sample. Over a 300s window the gain is
	// 299 m → VAM = 299 / (300/3600) h = 3588 m/h.
	base := time.Unix(1000, 0).UTC()
	samples := make([]domain.StreamSample, 400)
	for i := range samples {
		alt := float64(i)
		samples[i] = domain.StreamSample{Timestamp: base.Add(time.Duration(i) * time.Second), AltitudeM: &alt}
	}
	w := windowSpec{domain.BestEffortMetricVAM, domain.BestEffortWindowDuration, 300}
	be, ok := uc.computeVAMEffort(domain.Activity{}, domain.ActivitySource{}, samples, w, fixedNow())
	if !ok {
		t.Fatal("expected a VAM effort")
	}
	if be.Metric != domain.BestEffortMetricVAM {
		t.Fatalf("metric = %q", be.Metric)
	}
	if be.AchievedValue < 3580 || be.AchievedValue > 3596 {
		t.Fatalf("VAM = %.1f m/h, want ~3588", be.AchievedValue)
	}

	// Flat altitude → no climbing window → no effort.
	flat := make([]domain.StreamSample, 400)
	for i := range flat {
		alt := 100.0
		flat[i] = domain.StreamSample{Timestamp: base.Add(time.Duration(i) * time.Second), AltitudeM: &alt}
	}
	if _, ok := uc.computeVAMEffort(domain.Activity{}, domain.ActivitySource{}, flat, w, fixedNow()); ok {
		t.Fatal("flat altitude should produce no VAM effort")
	}
}
