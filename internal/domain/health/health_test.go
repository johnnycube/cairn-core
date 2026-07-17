package health

import (
	"testing"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/domain/capability"
)

func f(v float64) *float64 { return &v }

var day = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func TestMergeDaily_CascadePicksProvider(t *testing.T) {
	// HRV from whoop should win over garmin for the per-type override
	// [whoop, garmin, _any], even if garmin's sample is later in the day.
	samples := []Sample{
		{DataType: capability.DataTypeHRV, Timestamp: day.Add(8 * time.Hour), Provider: "garmin", Value: f(60)},
		{DataType: capability.DataTypeHRV, Timestamp: day.Add(6 * time.Hour), Provider: "whoop", Value: f(72)},
	}
	merged := MergeDaily(samples, []string{"whoop", "garmin", domain.AnyProvider})
	if len(merged) != 1 {
		t.Fatalf("want 1 merged, got %d", len(merged))
	}
	if merged[0].Provider != "whoop" || merged[0].Value != 72 {
		t.Errorf("HRV winner = %s/%v, want whoop/72", merged[0].Provider, merged[0].Value)
	}
}

func TestMergeDaily_LatestWithinProvider(t *testing.T) {
	// Same provider, same day → latest reading wins (most up-to-date weight).
	samples := []Sample{
		{DataType: capability.DataTypeWeight, Timestamp: day.Add(7 * time.Hour), Provider: "garmin", Value: f(80.5)},
		{DataType: capability.DataTypeWeight, Timestamp: day.Add(20 * time.Hour), Provider: "garmin", Value: f(80.1)},
	}
	merged := MergeDaily(samples, nil)
	if len(merged) != 1 || merged[0].Value != 80.1 {
		t.Errorf("weight = %+v, want latest 80.1", merged)
	}
}

func TestMergeDaily_SeparateDaysAndTypes(t *testing.T) {
	samples := []Sample{
		{DataType: capability.DataTypeSteps, Timestamp: day.Add(10 * time.Hour), Provider: "garmin", Value: f(8000)},
		{DataType: capability.DataTypeSteps, Timestamp: day.Add(34 * time.Hour), Provider: "garmin", Value: f(9000)}, // next day
		{DataType: capability.DataTypeRestingHR, Timestamp: day.Add(5 * time.Hour), Provider: "garmin", Value: f(48)},
	}
	merged := MergeDaily(samples, nil)
	if len(merged) != 3 {
		t.Fatalf("want 3 (2 step-days + 1 rhr), got %d", len(merged))
	}
}

func TestMergeDaily_SkipsNilValues(t *testing.T) {
	samples := []Sample{{DataType: capability.DataTypeHRV, Timestamp: day, Provider: "garmin", Value: nil}}
	if got := MergeDaily(samples, nil); len(got) != 0 {
		t.Errorf("nil-value samples should be skipped, got %d", len(got))
	}
}
