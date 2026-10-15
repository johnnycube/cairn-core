package domain

import (
	"testing"
	"time"
)

func TestStreamKeepChannels(t *testing.T) {
	f, f32, i16 := 1.5, float32(2.5), int16(3)
	full := StreamSample{
		Timestamp: time.Unix(10, 0), Latitude: &f, Longitude: &f, AltitudeM: &f, DistanceM: &f, SpeedMps: &f,
		HeartRateBpm: &i16, PowerW: &i16, Cadence: &i16, TemperatureC: &f32, Grade: &f32,
		LeftRightBalance: &f32, LeftTorqueEffectiveness: &f32, RightTorqueEffectiveness: &f32,
		LeftPedalSmoothness: &f32, RightPedalSmoothness: &f32, VerticalOscillationMm: &f32,
		GroundContactTimeMs: &i16, StrideLengthM: &f32, RespirationRateBrpm: &f32, CoreTemperatureC: &f32,
	}
	s := Stream{Resolution: StreamResolutionRaw, Samples: []StreamSample{full}}

	if got := s.KeepChannels(nil); got.Samples[0] != full {
		t.Error("nil channels must keep everything")
	}
	if got := s.KeepChannels(AllStreamChannels); got.Samples[0] != full {
		t.Error("all channels must keep everything")
	}
	if got := s.KeepChannels([]StreamChannel{}); got.Samples[0] != (StreamSample{Timestamp: full.Timestamp}) {
		t.Errorf("empty channels = %+v, want timestamp only", got.Samples[0])
	}
	got := s.KeepChannels([]StreamChannel{StreamChannelHeartRate, StreamChannelCoreTemperature})
	want := StreamSample{Timestamp: full.Timestamp, HeartRateBpm: &i16, CoreTemperatureC: &f32}
	if got.Samples[0] != want || got.Resolution != StreamResolutionRaw {
		t.Errorf("subset = %+v", got.Samples[0])
	}
	if s.Samples[0] != full {
		t.Error("input stream was modified")
	}
}
