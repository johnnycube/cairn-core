package domain

import "time"

// ---------------------------------------------------------------------------
// Stream
//
// A time-series of samples from a single ActivitySource. Streams are NOT
// merged across sources — multi-source activities expose one stream per
// source and the UI lets the user toggle which to render. This keeps each
// stream internally consistent (no Frankenstein-stitched GPS tracks).
//
// Samples are wide-shaped: one row per (activity_source_id, ts) with a
// nullable column per channel. The DB table is a TimescaleDB hypertable;
// the domain shape mirrors this for sqlc's convenience.
// ---------------------------------------------------------------------------

// Stream is the in-memory representation of a contiguous range of samples
// from one source. Callers usually request a Stream via the StreamRepo
// port; the underlying repository decides whether to read from the raw
// table or one of the continuous aggregates based on the StreamQuery's
// requested resolution.
type Stream struct {
	ActivitySourceID SourceID
	Resolution       StreamResolution
	Samples          []StreamSample
}

// StreamSample is a single point on the timeline. Pointer-typed channels
// distinguish "the source did not record this channel at this instant"
// from "the source recorded zero".
type StreamSample struct {
	Timestamp time.Time

	// Core channels — present in almost every stream.
	Latitude     *float64
	Longitude    *float64
	AltitudeM    *float64
	DistanceM    *float64 // cumulative
	SpeedMps     *float64
	HeartRateBpm *int16
	PowerW       *int16
	Cadence      *int16
	TemperatureC *float32
	Grade        *float32

	// Cycling-specific advanced metrics.
	LeftRightBalance         *float32
	LeftTorqueEffectiveness  *float32
	RightTorqueEffectiveness *float32
	LeftPedalSmoothness      *float32
	RightPedalSmoothness     *float32

	// Running-specific advanced metrics.
	VerticalOscillationMm *float32
	GroundContactTimeMs   *int16
	StrideLengthM         *float32

	// Other.
	RespirationRateBrpm *float32
	CoreTemperatureC    *float32
}

// ---------------------------------------------------------------------------
// StreamChannel — typed channel identifier
//
// Used in StreamQuery.Channels to request a subset of columns. The wire
// layer (cairn.v1.StreamChannel proto enum) maps 1:1 to these constants.
// ---------------------------------------------------------------------------

type StreamChannel string

const (
	StreamChannelLatitude    StreamChannel = "latitude"
	StreamChannelLongitude   StreamChannel = "longitude"
	StreamChannelAltitude    StreamChannel = "altitude"
	StreamChannelDistance    StreamChannel = "distance"
	StreamChannelSpeed       StreamChannel = "speed"
	StreamChannelHeartRate   StreamChannel = "heart_rate"
	StreamChannelPower       StreamChannel = "power"
	StreamChannelCadence     StreamChannel = "cadence"
	StreamChannelTemperature StreamChannel = "temperature"
	StreamChannelGrade       StreamChannel = "grade"

	StreamChannelLeftRightBalance         StreamChannel = "left_right_balance"
	StreamChannelLeftTorqueEffectiveness  StreamChannel = "left_torque_effectiveness"
	StreamChannelRightTorqueEffectiveness StreamChannel = "right_torque_effectiveness"
	StreamChannelLeftPedalSmoothness      StreamChannel = "left_pedal_smoothness"
	StreamChannelRightPedalSmoothness     StreamChannel = "right_pedal_smoothness"

	StreamChannelVerticalOscillation StreamChannel = "vertical_oscillation"
	StreamChannelGroundContactTime   StreamChannel = "ground_contact_time"
	StreamChannelStrideLength        StreamChannel = "stride_length"

	StreamChannelRespirationRate StreamChannel = "respiration_rate"
	StreamChannelCoreTemperature StreamChannel = "core_temperature"
)

// AllStreamChannels is the canonical iteration order; matches the column
// order in the activity_streams table (migration 00006).
var AllStreamChannels = []StreamChannel{
	StreamChannelLatitude, StreamChannelLongitude, StreamChannelAltitude,
	StreamChannelDistance, StreamChannelSpeed, StreamChannelHeartRate,
	StreamChannelPower, StreamChannelCadence, StreamChannelTemperature,
	StreamChannelGrade,

	StreamChannelLeftRightBalance, StreamChannelLeftTorqueEffectiveness,
	StreamChannelRightTorqueEffectiveness, StreamChannelLeftPedalSmoothness,
	StreamChannelRightPedalSmoothness,

	StreamChannelVerticalOscillation, StreamChannelGroundContactTime,
	StreamChannelStrideLength,

	StreamChannelRespirationRate, StreamChannelCoreTemperature,
}

// Valid reports whether c is one of the known channels.
func (c StreamChannel) Valid() bool {
	for _, k := range AllStreamChannels {
		if c == k {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// StreamResolution
//
// Maps to the storage layer's choice between raw rows and continuous
// aggregates. The repository decides; callers express intent.
// ---------------------------------------------------------------------------

type StreamResolution string

const (
	// StreamResolutionRaw queries the raw activity_streams table — full
	// fidelity, suitable for export and lap-boundary computations.
	StreamResolutionRaw StreamResolution = "raw"

	// StreamResolution5s queries the activity_streams_5s continuous aggregate.
	// Suitable for typical activity-page rendering.
	StreamResolution5s StreamResolution = "5s"

	// StreamResolution30s queries the activity_streams_30s continuous aggregate.
	// Suitable for very long activities where 5s yields too many points.
	StreamResolution30s StreamResolution = "30s"
)

// Valid reports whether r is one of the known resolutions.
func (r StreamResolution) Valid() bool {
	switch r {
	case StreamResolutionRaw, StreamResolution5s, StreamResolution30s:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// StreamQuery
//
// Parameters for fetching a Stream. Channels=nil means "all channels".
// Time-bounded queries are used by lap-boundary computations and segment
// matching; full-stream queries (StartTime/EndTime both zero) are used
// for chart rendering.
// ---------------------------------------------------------------------------

type StreamQuery struct {
	ActivitySourceID SourceID
	Channels         []StreamChannel
	StartTime        time.Time
	EndTime          time.Time
	Resolution       StreamResolution
}
