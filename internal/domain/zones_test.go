package domain

import (
	"math"
	"testing"
)

func TestBucketSecondsPower(t *testing.T) {
	bands := PowerZoneBands() // 7 bands vs FTP
	ftp := 200.0
	// values: 100W (0.50 → Z1), 150W (0.75 → Z3 boundary), 190W (0.95 → Z4),
	// 220W (1.10 → Z5), 350W (1.75 → Z7). Each 10s.
	vals := []float64{100, 150, 190, 220, 350}
	dts := []float64{10, 10, 10, 10, 10}
	got := BucketSeconds(bands, ftp, vals, dts)
	// Z1=10, Z2=0, Z3=10, Z4=10, Z5=10, Z6=0, Z7=10
	want := []float64{10, 0, 10, 10, 10, 0, 10}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.01 {
			t.Fatalf("band %d (%s): got %.1f want %.1f", i, bands[i].Label, got[i], want[i])
		}
	}
}

func TestBucketSecondsHRLTHR(t *testing.T) {
	bands := HRZoneBands("lthr")
	lthr := 170.0
	// 120 (0.71→Z1), 150 (0.88→Z2), 165 (0.97→Z4), 175 (1.03→Z5)
	vals := []float64{120, 150, 165, 175}
	dts := []float64{5, 5, 5, 5}
	got := BucketSeconds(bands, lthr, vals, dts)
	want := []float64{5, 5, 0, 5, 5} // Z1,Z2,Z3,Z4,Z5
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.01 {
			t.Fatalf("band %d (%s): got %.1f want %.1f", i, bands[i].Label, got[i], want[i])
		}
	}
}

func TestAerobicDecoupling(t *testing.T) {
	// Steady effort: same HR & output both halves → ~0% decoupling.
	hr := make([]float64, 40)
	out := make([]float64, 40)
	for i := range hr {
		hr[i] = 150
		out[i] = 200
	}
	d, ok := AerobicDecoupling(hr, out)
	if !ok || math.Abs(d) > 0.01 {
		t.Fatalf("steady: got %.3f ok=%v, want ~0", d, ok)
	}

	// HR drifts up 10% in the second half at the same power → positive decoupling.
	for i := 20; i < 40; i++ {
		hr[i] = 165
	}
	// EF1 = 200/150 = 1.333; EF2 = 200/165 = 1.212; decoupling = ~9.1%
	d, ok = AerobicDecoupling(hr, out)
	if !ok || d < 8 || d > 10 {
		t.Fatalf("drift: got %.2f, want ~9.1", d)
	}

	// Too few samples → not ok.
	if _, ok := AerobicDecoupling(hr[:10], out[:10]); ok {
		t.Fatal("expected ok=false for <20 samples")
	}
}

func TestBucketSecondsNoReference(t *testing.T) {
	got := BucketSeconds(PowerZoneBands(), 0, []float64{200}, []float64{10})
	for i, v := range got {
		if v != 0 {
			t.Fatalf("band %d should be 0 with no reference, got %.1f", i, v)
		}
	}
}
