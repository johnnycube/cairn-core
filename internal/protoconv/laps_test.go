package protoconv

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
)

func TestActivitySourcePayloadLaps(t *testing.T) {
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	p := &cairnv1.ActivitySourcePayload{
		StartTime: timestamppb.New(start),
		Laps: []*cairnv1.ActivityLap{
			{
				Index:           1,
				Label:           "Lap 1",
				StartTime:       timestamppb.New(start), // offset 0
				ElapsedDuration: durationpb.New(5 * time.Minute),
				MovingDuration:  durationpb.New(4 * time.Minute),
				DistanceM:       f64(1500),
				AvgHeartRateBpm: i32(150),
				AvgPowerW:       i32(220),
			},
			{
				Index:           2,
				Label:           "Lap 2",
				StartTime:       timestamppb.New(start.Add(5 * time.Minute)), // offset 5m
				ElapsedDuration: durationpb.New(6 * time.Minute),
				MovingDuration:  durationpb.New(6 * time.Minute),
			},
		},
	}

	out := ActivitySourcePayloadFromProto(p)
	if len(out.Laps) != 2 {
		t.Fatalf("expected 2 laps, got %d", len(out.Laps))
	}
	l0, l1 := out.Laps[0], out.Laps[1]
	if l0.Index != 1 || l0.Label != "Lap 1" {
		t.Fatalf("lap0 index/label: %d %q", l0.Index, l0.Label)
	}
	if l0.StartOffset != 0 {
		t.Fatalf("lap0 offset want 0, got %v", l0.StartOffset)
	}
	if l0.ElapsedDuration != 5*time.Minute || l0.MovingDuration != 4*time.Minute {
		t.Fatalf("lap0 durations: %v %v", l0.ElapsedDuration, l0.MovingDuration)
	}
	if l0.DistanceM == nil || *l0.DistanceM != 1500 {
		t.Fatalf("lap0 distance: %v", l0.DistanceM)
	}
	if l0.AvgHeartRateBpm == nil || *l0.AvgHeartRateBpm != 150 {
		t.Fatalf("lap0 hr: %v", l0.AvgHeartRateBpm)
	}
	if l1.StartOffset != 5*time.Minute {
		t.Fatalf("lap1 offset want 5m, got %v", l1.StartOffset)
	}
	// Unset optionals stay nil.
	if l1.DistanceM != nil || l1.AvgPowerW != nil {
		t.Fatalf("lap1 should have nil optionals, got dist=%v pwr=%v", l1.DistanceM, l1.AvgPowerW)
	}
}

func f64(v float64) *float64 { return &v }
func i32(v int32) *int32     { return &v }
