// Package protoconv converts the generated wire types (cairn.v1 / worker.v1)
// into domain types. It is the single home for proto→domain mapping on the
// import path — the worker emits typed proto, the Core decodes it here, and
// there is no hand-rolled JSON wire form anywhere.
package protoconv

import (
	"math"
	"time"

	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// ActivityTypeFromProto maps the wire enum to the domain value.
func ActivityTypeFromProto(t cairnv1.ActivityType) domain.ActivityType {
	switch t {
	case cairnv1.ActivityType_ACTIVITY_TYPE_RIDE:
		return domain.ActivityTypeRide
	case cairnv1.ActivityType_ACTIVITY_TYPE_RUN:
		return domain.ActivityTypeRun
	case cairnv1.ActivityType_ACTIVITY_TYPE_SWIM:
		return domain.ActivityTypeSwim
	case cairnv1.ActivityType_ACTIVITY_TYPE_HIKE:
		return domain.ActivityTypeHike
	case cairnv1.ActivityType_ACTIVITY_TYPE_WALK:
		return domain.ActivityTypeWalk
	case cairnv1.ActivityType_ACTIVITY_TYPE_ROW:
		return domain.ActivityTypeRow
	case cairnv1.ActivityType_ACTIVITY_TYPE_SKI:
		return domain.ActivityTypeSki
	case cairnv1.ActivityType_ACTIVITY_TYPE_WORKOUT:
		return domain.ActivityTypeWorkout
	}
	return domain.ActivityTypeWorkout // unspecified → generic workout
}

// DisciplineFromProto maps the wire enum to the domain value.
func DisciplineFromProto(d cairnv1.Discipline) domain.Discipline {
	switch d {
	case cairnv1.Discipline_DISCIPLINE_RIDE_ROAD:
		return domain.DisciplineRideRoad
	case cairnv1.Discipline_DISCIPLINE_RIDE_MTB:
		return domain.DisciplineRideMTB
	case cairnv1.Discipline_DISCIPLINE_RIDE_GRAVEL:
		return domain.DisciplineRideGravel
	case cairnv1.Discipline_DISCIPLINE_RIDE_CYCLOCROSS:
		return domain.DisciplineRideCyclocross
	case cairnv1.Discipline_DISCIPLINE_RIDE_TRACK:
		return domain.DisciplineRideTrack
	case cairnv1.Discipline_DISCIPLINE_RIDE_BMX:
		return domain.DisciplineRideBMX
	case cairnv1.Discipline_DISCIPLINE_RUN_ROAD:
		return domain.DisciplineRunRoad
	case cairnv1.Discipline_DISCIPLINE_RUN_TRAIL:
		return domain.DisciplineRunTrail
	case cairnv1.Discipline_DISCIPLINE_RUN_TRACK:
		return domain.DisciplineRunTrack
	case cairnv1.Discipline_DISCIPLINE_SWIM_POOL:
		return domain.DisciplineSwimPool
	case cairnv1.Discipline_DISCIPLINE_SWIM_OPEN_WATER:
		return domain.DisciplineSwimOpenWater
	case cairnv1.Discipline_DISCIPLINE_SKI_ALPINE:
		return domain.DisciplineSkiAlpine
	case cairnv1.Discipline_DISCIPLINE_SKI_NORDIC:
		return domain.DisciplineSkiNordic
	case cairnv1.Discipline_DISCIPLINE_SKI_TOURING:
		return domain.DisciplineSkiTouring
	case cairnv1.Discipline_DISCIPLINE_SKI_BACKCOUNTRY:
		return domain.DisciplineSkiBackcountry
	}
	return domain.DisciplineNone
}

// ActivitySourcePayloadFromProto maps a wire payload to the domain payload.
func ActivitySourcePayloadFromProto(p *cairnv1.ActivitySourcePayload) domain.ActivitySourcePayload {
	if p == nil {
		return domain.ActivitySourcePayload{}
	}
	out := domain.ActivitySourcePayload{
		Type:            ActivityTypeFromProto(p.GetType()),
		Discipline:      DisciplineFromProto(p.GetDiscipline()),
		IsVirtual:       p.GetIsVirtual(),
		IsEbike:         p.GetIsEbike(),
		IsCommute:       p.GetIsCommute(),
		IsRace:          p.GetIsRace(),
		CustomSubtype:   p.GetCustomSubtype(),
		Title:           p.GetTitle(),
		Description:     p.GetDescription(),
		ElapsedDuration: p.GetElapsedDuration().AsDuration(),
		MovingDuration:  p.GetMovingDuration().AsDuration(),
		Timezone:        p.GetTimezone(),
		Summary:         activitySummaryFromProto(p.GetSummary()),
	}
	if ts := p.GetStartTime(); ts != nil {
		out.StartTime = ts.AsTime()
	}
	if ts := p.GetEndTime(); ts != nil {
		out.EndTime = ts.AsTime()
	}
	out.Laps = activityLapsFromProto(p.GetLaps(), out.StartTime)
	return out
}

// activityLapsFromProto maps provider-reported laps. The domain models a lap's
// position as an offset from the activity start, while the wire carries an
// absolute start_time — convert here using activityStart.
func activityLapsFromProto(laps []*cairnv1.ActivityLap, activityStart time.Time) []domain.ActivityLap {
	if len(laps) == 0 {
		return nil
	}
	out := make([]domain.ActivityLap, 0, len(laps))
	for _, l := range laps {
		if l == nil {
			continue
		}
		lap := domain.ActivityLap{
			Index:           int(l.GetIndex()),
			Label:           l.GetLabel(),
			ElapsedDuration: l.GetElapsedDuration().AsDuration(),
			MovingDuration:  l.GetMovingDuration().AsDuration(),
		}
		if ts := l.GetStartTime(); ts != nil && !activityStart.IsZero() {
			lap.StartOffset = ts.AsTime().Sub(activityStart)
		}
		if l.DistanceM != nil {
			lap.DistanceM = domain.Ptr(l.GetDistanceM())
		}
		if l.AvgSpeedMps != nil {
			lap.AvgSpeedMps = domain.Ptr(l.GetAvgSpeedMps())
		}
		if l.AvgHeartRateBpm != nil {
			lap.AvgHeartRateBpm = domain.Ptr(int(l.GetAvgHeartRateBpm()))
		}
		if l.AvgPowerW != nil {
			lap.AvgPowerW = domain.Ptr(int(l.GetAvgPowerW()))
		}
		if l.AvgCadence != nil {
			lap.AvgCadence = domain.Ptr(int(l.GetAvgCadence()))
		}
		if l.ElevationGainM != nil {
			lap.ElevationGainM = domain.Ptr(l.GetElevationGainM())
		}
		out = append(out, lap)
	}
	return out
}

func activitySummaryFromProto(s *cairnv1.ActivitySummary) domain.ActivitySummary {
	if s == nil {
		return domain.ActivitySummary{}
	}
	return domain.ActivitySummary{
		DistanceM:        s.DistanceM,
		ElevationGainM:   s.ElevationGainM,
		ElevationLossM:   s.ElevationLossM,
		MinElevationM:    s.MinElevationM,
		MaxElevationM:    s.MaxElevationM,
		AvgSpeedMps:      s.AvgSpeedMps,
		MaxSpeedMps:      s.MaxSpeedMps,
		AvgHeartRateBpm:  i32ToIntPtr(s.AvgHeartRateBpm),
		MaxHeartRateBpm:  i32ToIntPtr(s.MaxHeartRateBpm),
		AvgPowerW:        i32ToIntPtr(s.AvgPowerW),
		MaxPowerW:        i32ToIntPtr(s.MaxPowerW),
		NormalizedPowerW: i32ToIntPtr(s.NormalizedPowerW),
		AvgCadence:       i32ToIntPtr(s.AvgCadence),
		MaxCadence:       i32ToIntPtr(s.MaxCadence),
		AvgTemperatureC:  s.AvgTemperatureC,
		MinTemperatureC:  s.MinTemperatureC,
		MaxTemperatureC:  s.MaxTemperatureC,
		CaloriesKcal:     i32ToIntPtr(s.CaloriesKcal),
		TSS:              s.Tss,
		IntensityFactor:  s.IntensityFactor,
		PoolLengthM:      i32ToIntPtr(s.PoolLengthM),
		TotalStrokes:     i32ToIntPtr(s.TotalStrokes),
	}
}

// ActivityStreamSamplesFromProto pivots the column-oriented wire stream into
// the domain's row-based samples. start is the activity start (the wire form
// carries time_s = seconds-since-start; the domain stores absolute time).
//
// A channel whose column is empty was not recorded (all-nil). Within a
// recorded float channel, NaN marks a per-sample gap (→ nil pointer); int
// channels are dense (0 is a real value, e.g. coasting power).
func ActivityStreamSamplesFromProto(s *cairnv1.ActivityStream, start time.Time) []domain.StreamSample {
	if s == nil || s.GetSampleCount() == 0 {
		return nil
	}
	n := int(s.GetSampleCount())
	samples := make([]domain.StreamSample, n)

	for i := 0; i < n; i++ {
		smp := domain.StreamSample{}
		if i < len(s.TimeS) {
			smp.Timestamp = start.Add(time.Duration(s.TimeS[i] * float64(time.Second)))
		} else {
			smp.Timestamp = start.Add(time.Duration(i) * time.Second)
		}
		smp.Latitude = f64At(s.Latitude, i)
		smp.Longitude = f64At(s.Longitude, i)
		smp.AltitudeM = f64At(s.AltitudeM, i)
		smp.DistanceM = f64At(s.DistanceM, i)
		smp.SpeedMps = f64At(s.SpeedMps, i)
		smp.HeartRateBpm = i32At16(s.HeartRateBpm, i)
		smp.PowerW = i32At16(s.PowerW, i)
		smp.Cadence = i32At16(s.Cadence, i)
		smp.TemperatureC = f64At32(s.TemperatureC, i)
		smp.Grade = f64At32(s.Grade, i)
		samples[i] = smp
	}
	return samples
}

// ---- small helpers ----

func i32ToIntPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// f64At returns &col[i] unless the channel is absent or the value is NaN.
func f64At(col []float64, i int) *float64 {
	if i >= len(col) || math.IsNaN(col[i]) {
		return nil
	}
	v := col[i]
	return &v
}

// f64At32 narrows a float64 column value to *float32.
func f64At32(col []float64, i int) *float32 {
	if i >= len(col) || math.IsNaN(col[i]) {
		return nil
	}
	v := float32(col[i])
	return &v
}

// i32At16 narrows an int32 column value to *int16 (dense channel: every
// in-range index has a value).
func i32At16(col []int32, i int) *int16 {
	if i >= len(col) {
		return nil
	}
	v := int16(col[i])
	return &v
}
