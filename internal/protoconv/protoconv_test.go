package protoconv

import (
	"math"
	"testing"
	"time"

	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

func TestActivityStreamSamplesFromProto(t *testing.T) {
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	s := &cairnv1.ActivityStream{
		SampleCount:  3,
		TimeS:        []float64{0, 2},
		Latitude:     []float64{47.1, math.NaN(), 47.3},
		HeartRateBpm: []int32{120, 40000},
		TemperatureC: []float64{21.5},
	}
	got := ActivityStreamSamplesFromProto(s, start)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if !got[1].Timestamp.Equal(start.Add(2*time.Second)) || !got[2].Timestamp.Equal(start.Add(2*time.Second)) {
		t.Errorf("timestamps = %v, %v", got[1].Timestamp, got[2].Timestamp)
	}
	if got[0].Latitude == nil || *got[0].Latitude != 47.1 || got[1].Latitude != nil {
		t.Errorf("latitude gap not mapped to nil")
	}
	if got[1].HeartRateBpm == nil || *got[1].HeartRateBpm != math.MaxInt16 || got[2].HeartRateBpm != nil {
		t.Errorf("heart rate not clamped/absent: %v", got[1].HeartRateBpm)
	}
	if got[0].TemperatureC == nil || *got[0].TemperatureC != 21.5 || got[0].Longitude != nil {
		t.Errorf("channels mis-mapped")
	}
}

func TestActivityStreamSamplesFromProto_RejectsEmptyAndOversized(t *testing.T) {
	if ActivityStreamSamplesFromProto(nil, time.Time{}) != nil {
		t.Error("nil stream")
	}
	if ActivityStreamSamplesFromProto(&cairnv1.ActivityStream{}, time.Time{}) != nil {
		t.Error("zero sample_count")
	}
	if ActivityStreamSamplesFromProto(&cairnv1.ActivityStream{SampleCount: MaxStreamSamples + 1}, time.Time{}) != nil {
		t.Error("oversized sample_count must not allocate")
	}
}

func TestEnumsFromProto(t *testing.T) {
	if ActivityTypeFromProto(cairnv1.ActivityType_ACTIVITY_TYPE_RUN) != domain.ActivityTypeRun ||
		ActivityTypeFromProto(cairnv1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED) != domain.ActivityTypeWorkout {
		t.Error("activity type")
	}
	for v := range cairnv1.ActivityType_name {
		_ = ActivityTypeFromProto(cairnv1.ActivityType(v))
	}
	if DisciplineFromProto(cairnv1.Discipline_DISCIPLINE_RUN_TRAIL) != domain.DisciplineRunTrail ||
		DisciplineFromProto(cairnv1.Discipline_DISCIPLINE_UNSPECIFIED) != domain.DisciplineNone {
		t.Error("discipline")
	}
	for v := range cairnv1.Discipline_name {
		_ = DisciplineFromProto(cairnv1.Discipline(v))
	}
	if ClimbCategoryFromProto(cairnv1.ClimbCategory_CLIMB_CATEGORY_HORS_CATEGORIE) != domain.ClimbCategoryHorsCategorie {
		t.Error("climb category")
	}
	for v := range cairnv1.ClimbCategory_name {
		_ = ClimbCategoryFromProto(cairnv1.ClimbCategory(v))
	}
}

func TestActivitySummaryFromProto(t *testing.T) {
	if (activitySummaryFromProto(nil) != domain.ActivitySummary{}) {
		t.Error("nil summary")
	}
	got := activitySummaryFromProto(&cairnv1.ActivitySummary{DistanceM: f64(10), AvgPowerW: i32(200)})
	if got.DistanceM == nil || *got.DistanceM != 10 || got.AvgPowerW == nil || *got.AvgPowerW != 200 || got.MaxPowerW != nil {
		t.Errorf("summary = %+v", got)
	}
	if p := ActivitySourcePayloadFromProto(nil); p.Title != "" || p.Laps != nil {
		t.Error("nil payload")
	}
}
