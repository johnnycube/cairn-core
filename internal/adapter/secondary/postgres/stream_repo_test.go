package postgres

import (
	"testing"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

func TestDedupeSamplesByTimestamp(t *testing.T) {
	t0 := time.Date(2024, 5, 6, 18, 29, 24, 0, time.UTC)
	f := func(v float64) *float64 { return &v }
	sec := func(off int, power float64) domain.StreamSample {
		return domain.StreamSample{Timestamp: t0.Add(time.Duration(off) * time.Second), PowerW: nil, SpeedMps: f(power)}
	}

	in := []domain.StreamSample{
		sec(0, 1), sec(1, 2),
		sec(2, 3), sec(2, 4), // duplicate ts — last wins
		sec(3, 5),
	}
	out := dedupeSamplesByTimestamp(in)
	if len(out) != 4 {
		t.Fatalf("len = %d; want 4", len(out))
	}
	if !out[2].Timestamp.Equal(t0.Add(2 * time.Second)) || *out[2].SpeedMps != 4 {
		t.Errorf("duplicate not collapsed to last occurrence: ts=%v speed=%v", out[2].Timestamp, *out[2].SpeedMps)
	}
	if !out[3].Timestamp.Equal(t0.Add(3 * time.Second)) {
		t.Errorf("order not preserved: %v", out[3].Timestamp)
	}

	// No duplicates → same slice back, no copy.
	clean := []domain.StreamSample{sec(0, 1), sec(1, 2)}
	if got := dedupeSamplesByTimestamp(clean); len(got) != 2 {
		t.Fatalf("clean input modified: len %d", len(got))
	}
}
