package connect

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// activityToProto converts a merged domain.Activity to its proto twin.
// Pointer fields on the summary stay as `optional` proto fields and are
// only set when the source provided a value.
func activityToProto(a domain.Activity, sources []domain.ActivitySource) *v1.Activity {
	out := &v1.Activity{
		Id:              a.ID.String(),
		UserId:          a.UserID.String(),
		Type:            activityTypeToProto(a.Type),
		Discipline:      disciplineToProto(a.Discipline),
		IsVirtual:       a.IsVirtual,
		IsEbike:         a.IsEbike,
		IsCommute:       a.IsCommute,
		IsRace:          a.IsRace,
		CustomSubtype:   a.CustomSubtype,
		Title:           a.Title,
		Description:     a.Description,
		StartTime:       timestamppb.New(a.StartTime),
		EndTime:         timestamppb.New(a.EndTime),
		ElapsedDuration: durationpb.New(a.ElapsedDuration),
		MovingDuration:  durationpb.New(a.MovingDuration),
		Timezone:        a.Timezone,
		Summary:         summaryToProto(a.Summary),
		MergeProvenance: provenanceToProto(a.MergeProvenance),
		MergedAt:        timestamppb.New(a.MergedAt),
		CreatedAt:       timestamppb.New(a.CreatedAt),
		UpdatedAt:       timestamppb.New(a.UpdatedAt),
		Tags:            append([]string(nil), a.Tags...),
		Privacy:         privacyToProto(a.Privacy),
		StartPlace:      a.StartPlace,
		StartLat:        a.StartLat,
		StartLng:        a.StartLng,
	}
	if a.PrimaryStreamSourceID != nil {
		out.PrimaryStreamSourceId = a.PrimaryStreamSourceID.String()
	}
	if a.GearID != nil {
		out.GearId = a.GearID.String()
	}
	if a.DeletedAt != nil {
		out.DeletedAt = timestamppb.New(*a.DeletedAt)
	}
	if len(sources) > 0 {
		out.Sources = make([]*v1.ActivitySource, len(sources))
		for i, s := range sources {
			out.Sources[i] = sourceToProto(s)
		}
	}
	return out
}

func sourceToProto(s domain.ActivitySource) *v1.ActivitySource {
	out := &v1.ActivitySource{
		Id:                   s.ID.String(),
		ActivityId:           s.ActivityID.String(),
		Provider:             s.Provider,
		ExternalId:           s.ExternalID,
		SourceWorkerName:     s.SourceWorkerName,
		SourceWorkerVersion:  s.SourceWorkerVersion,
		SourceWorkerPackage:  s.SourceWorkerPackage,
		RawBlobId:            s.RawBlobID,
		RawContentType:       s.RawContentType,
		RawSizeBytes:         s.RawSizeBytes,
		Status:               sourceStatusToProto(s.Status),
		StatusReason:         s.StatusReason,
		ReimportStatus:       reimportStatusToProto(s.ReimportStatus),
		ReimportStatusReason: s.ReimportStatusReason,
		ImportedAt:           timestamppb.New(s.ImportedAt),
		UpdatedAt:            timestamppb.New(s.UpdatedAt),
	}
	if s.ExternalAccountID != nil {
		out.ExternalAccountId = s.ExternalAccountID.String()
	}
	if s.LastReimportedAt != nil {
		out.LastReimportedAt = timestamppb.New(*s.LastReimportedAt)
	}
	return out
}

func summaryToProto(s domain.ActivitySummary) *v1.ActivitySummary {
	out := &v1.ActivitySummary{
		DistanceM:       s.DistanceM,
		ElevationGainM:  s.ElevationGainM,
		ElevationLossM:  s.ElevationLossM,
		MinElevationM:   s.MinElevationM,
		MaxElevationM:   s.MaxElevationM,
		AvgSpeedMps:     s.AvgSpeedMps,
		MaxSpeedMps:     s.MaxSpeedMps,
		AvgTemperatureC: s.AvgTemperatureC,
		MinTemperatureC: s.MinTemperatureC,
		MaxTemperatureC: s.MaxTemperatureC,
		Tss:             s.TSS,
		IntensityFactor: s.IntensityFactor,
	}
	out.AvgHeartRateBpm = intPtrToInt32(s.AvgHeartRateBpm)
	out.MaxHeartRateBpm = intPtrToInt32(s.MaxHeartRateBpm)
	out.AvgPowerW = intPtrToInt32(s.AvgPowerW)
	out.MaxPowerW = intPtrToInt32(s.MaxPowerW)
	out.NormalizedPowerW = intPtrToInt32(s.NormalizedPowerW)
	out.AvgCadence = intPtrToInt32(s.AvgCadence)
	out.MaxCadence = intPtrToInt32(s.MaxCadence)
	out.CaloriesKcal = intPtrToInt32(s.CaloriesKcal)
	out.PoolLengthM = intPtrToInt32(s.PoolLengthM)
	out.TotalStrokes = intPtrToInt32(s.TotalStrokes)
	return out
}

func intPtrToInt32(v *int) *int32 {
	if v == nil {
		return nil
	}
	cast := int32(*v)
	return &cast
}

func provenanceToProto(p domain.MergeProvenance) map[string]string {
	if len(p) == 0 {
		return nil
	}
	out := make(map[string]string, len(p))
	for fg, sid := range p {
		out[string(fg)] = sid.String()
	}
	return out
}

// ---------------------------------------------------------------------------
// Enum maps (domain → proto). Reverse direction is added when an update RPC
// arrives that needs it; keeping the maps one-way for now keeps the diff
// small.
// ---------------------------------------------------------------------------

func activityTypeToProto(t domain.ActivityType) v1.ActivityType {
	switch t {
	case domain.ActivityTypeRide:
		return v1.ActivityType_ACTIVITY_TYPE_RIDE
	case domain.ActivityTypeRun:
		return v1.ActivityType_ACTIVITY_TYPE_RUN
	case domain.ActivityTypeSwim:
		return v1.ActivityType_ACTIVITY_TYPE_SWIM
	case domain.ActivityTypeHike:
		return v1.ActivityType_ACTIVITY_TYPE_HIKE
	case domain.ActivityTypeWalk:
		return v1.ActivityType_ACTIVITY_TYPE_WALK
	case domain.ActivityTypeRow:
		return v1.ActivityType_ACTIVITY_TYPE_ROW
	case domain.ActivityTypeSki:
		return v1.ActivityType_ACTIVITY_TYPE_SKI
	case domain.ActivityTypeWorkout:
		return v1.ActivityType_ACTIVITY_TYPE_WORKOUT
	case domain.ActivityTypeSnowboard:
		return v1.ActivityType_ACTIVITY_TYPE_SNOWBOARD
	case domain.ActivityTypeSkate:
		return v1.ActivityType_ACTIVITY_TYPE_SKATE
	case domain.ActivityTypeKayak:
		return v1.ActivityType_ACTIVITY_TYPE_KAYAK
	case domain.ActivityTypeSUP:
		return v1.ActivityType_ACTIVITY_TYPE_SUP
	case domain.ActivityTypeSurf:
		return v1.ActivityType_ACTIVITY_TYPE_SURF
	case domain.ActivityTypeGolf:
		return v1.ActivityType_ACTIVITY_TYPE_GOLF
	case domain.ActivityTypeClimb:
		return v1.ActivityType_ACTIVITY_TYPE_CLIMB
	case domain.ActivityTypeTennis:
		return v1.ActivityType_ACTIVITY_TYPE_TENNIS
	case domain.ActivityTypeElliptical:
		return v1.ActivityType_ACTIVITY_TYPE_ELLIPTICAL
	case domain.ActivityTypeWheelchair:
		return v1.ActivityType_ACTIVITY_TYPE_WHEELCHAIR
	}
	return v1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED
}

// activityTypeFromProto maps the wire enum back to the domain value. ok is
// false for UNSPECIFIED / unknown so callers can reject an edit that didn't
// carry a real type.
func activityTypeFromProto(t v1.ActivityType) (domain.ActivityType, bool) {
	switch t {
	case v1.ActivityType_ACTIVITY_TYPE_RIDE:
		return domain.ActivityTypeRide, true
	case v1.ActivityType_ACTIVITY_TYPE_RUN:
		return domain.ActivityTypeRun, true
	case v1.ActivityType_ACTIVITY_TYPE_SWIM:
		return domain.ActivityTypeSwim, true
	case v1.ActivityType_ACTIVITY_TYPE_HIKE:
		return domain.ActivityTypeHike, true
	case v1.ActivityType_ACTIVITY_TYPE_WALK:
		return domain.ActivityTypeWalk, true
	case v1.ActivityType_ACTIVITY_TYPE_ROW:
		return domain.ActivityTypeRow, true
	case v1.ActivityType_ACTIVITY_TYPE_SKI:
		return domain.ActivityTypeSki, true
	case v1.ActivityType_ACTIVITY_TYPE_WORKOUT:
		return domain.ActivityTypeWorkout, true
	case v1.ActivityType_ACTIVITY_TYPE_SNOWBOARD:
		return domain.ActivityTypeSnowboard, true
	case v1.ActivityType_ACTIVITY_TYPE_SKATE:
		return domain.ActivityTypeSkate, true
	case v1.ActivityType_ACTIVITY_TYPE_KAYAK:
		return domain.ActivityTypeKayak, true
	case v1.ActivityType_ACTIVITY_TYPE_SUP:
		return domain.ActivityTypeSUP, true
	case v1.ActivityType_ACTIVITY_TYPE_SURF:
		return domain.ActivityTypeSurf, true
	case v1.ActivityType_ACTIVITY_TYPE_GOLF:
		return domain.ActivityTypeGolf, true
	case v1.ActivityType_ACTIVITY_TYPE_CLIMB:
		return domain.ActivityTypeClimb, true
	case v1.ActivityType_ACTIVITY_TYPE_TENNIS:
		return domain.ActivityTypeTennis, true
	case v1.ActivityType_ACTIVITY_TYPE_ELLIPTICAL:
		return domain.ActivityTypeElliptical, true
	case v1.ActivityType_ACTIVITY_TYPE_WHEELCHAIR:
		return domain.ActivityTypeWheelchair, true
	}
	return "", false
}

func disciplineToProto(d domain.Discipline) v1.Discipline {
	switch d {
	case domain.DisciplineRideRoad:
		return v1.Discipline_DISCIPLINE_RIDE_ROAD
	case domain.DisciplineRideMTB:
		return v1.Discipline_DISCIPLINE_RIDE_MTB
	case domain.DisciplineRideGravel:
		return v1.Discipline_DISCIPLINE_RIDE_GRAVEL
	case domain.DisciplineRideCyclocross:
		return v1.Discipline_DISCIPLINE_RIDE_CYCLOCROSS
	case domain.DisciplineRideTrack:
		return v1.Discipline_DISCIPLINE_RIDE_TRACK
	case domain.DisciplineRideBMX:
		return v1.Discipline_DISCIPLINE_RIDE_BMX
	case domain.DisciplineRunRoad:
		return v1.Discipline_DISCIPLINE_RUN_ROAD
	case domain.DisciplineRunTrail:
		return v1.Discipline_DISCIPLINE_RUN_TRAIL
	case domain.DisciplineRunTrack:
		return v1.Discipline_DISCIPLINE_RUN_TRACK
	case domain.DisciplineSwimPool:
		return v1.Discipline_DISCIPLINE_SWIM_POOL
	case domain.DisciplineSwimOpenWater:
		return v1.Discipline_DISCIPLINE_SWIM_OPEN_WATER
	case domain.DisciplineSkiAlpine:
		return v1.Discipline_DISCIPLINE_SKI_ALPINE
	case domain.DisciplineSkiNordic:
		return v1.Discipline_DISCIPLINE_SKI_NORDIC
	case domain.DisciplineSkiTouring:
		return v1.Discipline_DISCIPLINE_SKI_TOURING
	case domain.DisciplineSkiBackcountry:
		return v1.Discipline_DISCIPLINE_SKI_BACKCOUNTRY
	}
	return v1.Discipline_DISCIPLINE_UNSPECIFIED
}

// disciplineFromProto maps the wire enum back to the domain value. ok is false
// for UNSPECIFIED / unknown so callers can treat "clear" distinctly.
func disciplineFromProto(d v1.Discipline) (domain.Discipline, bool) {
	switch d {
	case v1.Discipline_DISCIPLINE_RIDE_ROAD:
		return domain.DisciplineRideRoad, true
	case v1.Discipline_DISCIPLINE_RIDE_MTB:
		return domain.DisciplineRideMTB, true
	case v1.Discipline_DISCIPLINE_RIDE_GRAVEL:
		return domain.DisciplineRideGravel, true
	case v1.Discipline_DISCIPLINE_RIDE_CYCLOCROSS:
		return domain.DisciplineRideCyclocross, true
	case v1.Discipline_DISCIPLINE_RIDE_TRACK:
		return domain.DisciplineRideTrack, true
	case v1.Discipline_DISCIPLINE_RIDE_BMX:
		return domain.DisciplineRideBMX, true
	case v1.Discipline_DISCIPLINE_RUN_ROAD:
		return domain.DisciplineRunRoad, true
	case v1.Discipline_DISCIPLINE_RUN_TRAIL:
		return domain.DisciplineRunTrail, true
	case v1.Discipline_DISCIPLINE_RUN_TRACK:
		return domain.DisciplineRunTrack, true
	case v1.Discipline_DISCIPLINE_SWIM_POOL:
		return domain.DisciplineSwimPool, true
	case v1.Discipline_DISCIPLINE_SWIM_OPEN_WATER:
		return domain.DisciplineSwimOpenWater, true
	case v1.Discipline_DISCIPLINE_SKI_ALPINE:
		return domain.DisciplineSkiAlpine, true
	case v1.Discipline_DISCIPLINE_SKI_NORDIC:
		return domain.DisciplineSkiNordic, true
	case v1.Discipline_DISCIPLINE_SKI_TOURING:
		return domain.DisciplineSkiTouring, true
	case v1.Discipline_DISCIPLINE_SKI_BACKCOUNTRY:
		return domain.DisciplineSkiBackcountry, true
	}
	return "", false
}

func privacyToProto(p domain.ActivityPrivacy) v1.Privacy {
	switch p {
	case domain.PrivacyPrivate:
		return v1.Privacy_PRIVACY_PRIVATE
	case domain.PrivacyFollowers:
		return v1.Privacy_PRIVACY_FOLLOWERS
	case domain.PrivacyPublic:
		return v1.Privacy_PRIVACY_PUBLIC
	}
	return v1.Privacy_PRIVACY_UNSPECIFIED
}

// privacyFromProto maps the wire enum back to the domain value. ok is false
// for UNSPECIFIED / unknown so the caller can reject an explicit privacy edit
// that didn't carry a real value.
func privacyFromProto(p v1.Privacy) (domain.ActivityPrivacy, bool) {
	switch p {
	case v1.Privacy_PRIVACY_PRIVATE:
		return domain.PrivacyPrivate, true
	case v1.Privacy_PRIVACY_FOLLOWERS:
		return domain.PrivacyFollowers, true
	case v1.Privacy_PRIVACY_PUBLIC:
		return domain.PrivacyPublic, true
	}
	return "", false
}

func sourceStatusToProto(s domain.SourceStatus) v1.ActivitySourceStatus {
	switch s {
	case domain.SourceStatusActive:
		return v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ACTIVE
	case domain.SourceStatusOrphaned:
		return v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ORPHANED
	case domain.SourceStatusAccountUnavailable:
		return v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ACCOUNT_UNAVAILABLE
	case domain.SourceStatusDetached:
		return v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_DETACHED
	}
	return v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_UNSPECIFIED
}

func reimportStatusToProto(s domain.ReimportStatus) v1.ReimportStatus {
	switch s {
	case domain.ReimportStatusCurrent:
		return v1.ReimportStatus_REIMPORT_STATUS_CURRENT
	case domain.ReimportStatusUpdateAvailable:
		return v1.ReimportStatus_REIMPORT_STATUS_UPDATE_AVAILABLE
	case domain.ReimportStatusUpdating:
		return v1.ReimportStatus_REIMPORT_STATUS_UPDATING
	case domain.ReimportStatusFailed:
		return v1.ReimportStatus_REIMPORT_STATUS_FAILED
	}
	return v1.ReimportStatus_REIMPORT_STATUS_UNSPECIFIED
}

// ---------------------------------------------------------------------------
// Stream conversion
//
// The proto ActivityStream is column-oriented: one parallel array per
// channel, lengths all matching sample_count. The domain Stream is the
// row-oriented form (one StreamSample per timestamp with nullable
// per-channel fields).
//
// We pivot: walk every sample once, transposing into per-channel
// columns. Channels that no sample populates are left empty and *not*
// listed in `channels` — clients only render arrays the response
// declares as present.
//
// Nullable values within a populated channel: float columns use NaN
// for "missing"; int columns use 0 (the proto repeated int32 has no
// other in-band null sentinel — clients treat 0 in heart_rate_bpm as
// "no reading" by convention).
// ---------------------------------------------------------------------------

func streamToProto(s domain.Stream) *v1.ActivityStream {
	out := &v1.ActivityStream{
		ActivitySourceId: s.ActivitySourceID.String(),
		SampleCount:      int32(len(s.Samples)),
		ResolutionHz:     resolutionHz(s.Resolution),
	}
	if len(s.Samples) == 0 {
		return out
	}

	n := len(s.Samples)
	base := s.Samples[0].Timestamp

	timeS := make([]float64, n)
	var (
		lat, lon, alt, dist, speed []float64
		hr, power, cadence         []int32
		temp, grade                []float64
	)

	for i, sample := range s.Samples {
		timeS[i] = sample.Timestamp.Sub(base).Seconds()
		lat = appendFloat(lat, sample.Latitude, n, i)
		lon = appendFloat(lon, sample.Longitude, n, i)
		alt = appendFloat(alt, sample.AltitudeM, n, i)
		dist = appendFloat(dist, sample.DistanceM, n, i)
		speed = appendFloat(speed, sample.SpeedMps, n, i)
		hr = appendInt16(hr, sample.HeartRateBpm, n, i)
		power = appendInt16(power, sample.PowerW, n, i)
		cadence = appendInt16(cadence, sample.Cadence, n, i)
		temp = appendFloat32(temp, sample.TemperatureC, n, i)
		grade = appendFloat32(grade, sample.Grade, n, i)
	}

	out.TimeS = timeS
	out.Channels = []v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_TIME}

	if lat != nil {
		out.Latitude = lat
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_LATITUDE)
	}
	if lon != nil {
		out.Longitude = lon
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_LONGITUDE)
	}
	if alt != nil {
		out.AltitudeM = alt
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_ALTITUDE)
	}
	if dist != nil {
		out.DistanceM = dist
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_DISTANCE)
	}
	if speed != nil {
		out.SpeedMps = speed
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_SPEED)
	}
	if hr != nil {
		out.HeartRateBpm = hr
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_HEART_RATE)
	}
	if power != nil {
		out.PowerW = power
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_POWER)
	}
	if cadence != nil {
		out.Cadence = cadence
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_CADENCE)
	}
	if temp != nil {
		out.TemperatureC = temp
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_TEMPERATURE)
	}
	if grade != nil {
		out.Grade = grade
		out.Channels = append(out.Channels, v1.StreamChannel_STREAM_CHANNEL_GRADE)
	}

	return out
}

// appendFloat lazily allocates the column on first non-nil value, then
// fills the gap from index 0 with NaN before recording v. This keeps
// channels that no sample populates from allocating at all.
func appendFloat(col []float64, v *float64, total, i int) []float64 {
	if v == nil {
		if col != nil {
			col = append(col, nan())
		}
		return col
	}
	if col == nil {
		col = make([]float64, 0, total)
		for k := 0; k < i; k++ {
			col = append(col, nan())
		}
	}
	return append(col, *v)
}

func appendFloat32(col []float64, v *float32, total, i int) []float64 {
	if v == nil {
		if col != nil {
			col = append(col, nan())
		}
		return col
	}
	if col == nil {
		col = make([]float64, 0, total)
		for k := 0; k < i; k++ {
			col = append(col, nan())
		}
	}
	return append(col, float64(*v))
}

func appendInt16(col []int32, v *int16, total, i int) []int32 {
	if v == nil {
		if col != nil {
			col = append(col, 0)
		}
		return col
	}
	if col == nil {
		col = make([]int32, 0, total)
		for k := 0; k < i; k++ {
			col = append(col, 0)
		}
	}
	return append(col, int32(*v))
}

func nan() float64 {
	// math.NaN inlined to avoid the math import — only needed for missing
	// float values in column-oriented stream output.
	var zero float64
	return zero / zero
}

// resolutionHz maps the domain resolution to the Hz the proto reports.
// Raw = 1Hz, 5s-CAgg = 0.2Hz, 30s-CAgg ≈ 0.033Hz.
func resolutionHz(r domain.StreamResolution) float64 {
	switch r {
	case domain.StreamResolutionRaw:
		return 1.0
	case domain.StreamResolution5s:
		return 0.2
	case domain.StreamResolution30s:
		return 1.0 / 30.0
	}
	return 0
}

// time.Time placeholder kept reachable so the import isn't flagged when
// we add reverse-direction conversions later.
var _ = time.Time{}

// ---------------------------------------------------------------------------
// BestEffort conversion
// ---------------------------------------------------------------------------

func bestEffortToProto(b domain.BestEffort) *v1.BestEffort {
	out := &v1.BestEffort{
		Id:               b.ID.String(),
		ActivityId:       b.ActivityID.String(),
		UserId:           b.UserID.String(),
		ActivityType:     activityTypeToProto(b.ActivityType),
		Discipline:       disciplineToProto(b.Discipline),
		Metric:           bestEffortMetricToProto(b.Metric),
		WindowKind:       bestEffortWindowKindToProto(b.WindowKind),
		WindowValue:      int32(b.WindowValue),
		AchievedValue:    b.AchievedValue,
		StartOffset:      int32(b.StartOffset),
		Duration:         durationpb.New(time.Duration(b.DurationS * float64(time.Second))),
		Ts:               timestamppb.New(b.Timestamp),
		ActivitySourceId: b.ActivitySourceID.String(),
	}
	if b.DistanceM != nil {
		out.DistanceM = b.DistanceM
	}
	return out
}

func bestEffortMetricToProto(m domain.BestEffortMetric) v1.BestEffortMetric {
	switch m {
	case domain.BestEffortMetricPace:
		return v1.BestEffortMetric_BEST_EFFORT_METRIC_PACE
	case domain.BestEffortMetricSpeed:
		return v1.BestEffortMetric_BEST_EFFORT_METRIC_SPEED
	case domain.BestEffortMetricPower:
		return v1.BestEffortMetric_BEST_EFFORT_METRIC_POWER
	case domain.BestEffortMetricHeartRate:
		return v1.BestEffortMetric_BEST_EFFORT_METRIC_HEART_RATE
	case domain.BestEffortMetricVAM:
		return v1.BestEffortMetric_BEST_EFFORT_METRIC_VAM
	}
	return v1.BestEffortMetric_BEST_EFFORT_METRIC_UNSPECIFIED
}

func bestEffortWindowKindToProto(k domain.BestEffortWindowKind) v1.BestEffortWindowKind {
	switch k {
	case domain.BestEffortWindowDistance:
		return v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_DISTANCE
	case domain.BestEffortWindowDuration:
		return v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_DURATION
	}
	return v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_UNSPECIFIED
}

// bestEffortMetricFromProto is the request-direction map. UNSPECIFIED in
// the request means "no filter" — callers handle that case before
// dispatching to this function.
func bestEffortMetricFromProto(m v1.BestEffortMetric) (domain.BestEffortMetric, bool) {
	switch m {
	case v1.BestEffortMetric_BEST_EFFORT_METRIC_PACE:
		return domain.BestEffortMetricPace, true
	case v1.BestEffortMetric_BEST_EFFORT_METRIC_SPEED:
		return domain.BestEffortMetricSpeed, true
	case v1.BestEffortMetric_BEST_EFFORT_METRIC_POWER:
		return domain.BestEffortMetricPower, true
	case v1.BestEffortMetric_BEST_EFFORT_METRIC_HEART_RATE:
		return domain.BestEffortMetricHeartRate, true
	case v1.BestEffortMetric_BEST_EFFORT_METRIC_VAM:
		return domain.BestEffortMetricVAM, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Notification conversion
//
// The domain enum is small (7 values) and stores per-event meaning; the
// proto enum is rich (40+ values, grouped by topic). We map domain → the
// most-specific proto value that fits. Unmapped domain types become
// UNSPECIFIED so the frontend falls back to the i18n title key.
// ---------------------------------------------------------------------------

func notificationToProto(n domain.Notification) *v1.NotificationEvent {
	out := &v1.NotificationEvent{
		Id:            n.ID.String(),
		UserId:        n.UserID.String(),
		Type:          notificationTypeToProto(n.Type),
		Severity:      notificationSeverityToProto(n.Severity),
		TitleI18NKey:  n.TitleI18nKey,
		BodyI18NKey:   n.BodyI18nKey,
		I18NParams:    n.I18nParams,
		DedupKey:      n.DedupKey,
		CoalesceCount: int32(n.CoalesceCount),
		Read:          n.Read,
		CreatedAt:     timestamppb.New(n.CreatedAt),
		UpdatedAt:     timestamppb.New(n.UpdatedAt),
	}
	if n.ActivityID != nil {
		v := n.ActivityID.String()
		out.ActivityId = &v
	}
	if n.SegmentID != nil {
		v := n.SegmentID.String()
		out.SegmentId = &v
	}
	if n.ExternalAccountID != nil {
		v := n.ExternalAccountID.String()
		out.ExternalAccountId = &v
	}
	if n.WorkerName != "" {
		v := n.WorkerName
		out.WorkerName = &v
	}
	if n.ReadAt != nil {
		out.ReadAt = timestamppb.New(*n.ReadAt)
	}
	return out
}

func notificationTypeToProto(t domain.NotificationType) v1.NotificationEventType {
	switch t {
	case domain.NotificationTypeSegmentPersonalRecord:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACHIEVEMENT_SEGMENT_PR
	case domain.NotificationTypeSegmentInstanceRecord:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACHIEVEMENT_SEGMENT_KOM
	case domain.NotificationTypeActivityImported:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_BULK_IMPORT_COMPLETED
	case domain.NotificationTypeActivityReimported:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_AUTO_REIMPORT_COMPLETED
	case domain.NotificationTypeWorkerOffline:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_WORKER_OFFLINE_FOR_ACCOUNT
	case domain.NotificationTypeExternalAccountRefreshFailed:
		return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACCOUNT_EXTERNAL_AUTH_INVALID
	}
	return v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_UNSPECIFIED
}

func notificationSeverityToProto(s domain.NotificationSeverity) v1.NotificationSeverity {
	switch s {
	case domain.NotificationSeverityInfo:
		return v1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO
	case domain.NotificationSeverityWarn:
		return v1.NotificationSeverity_NOTIFICATION_SEVERITY_WARN
	case domain.NotificationSeverityError:
		return v1.NotificationSeverity_NOTIFICATION_SEVERITY_ERROR
	}
	return v1.NotificationSeverity_NOTIFICATION_SEVERITY_UNSPECIFIED
}

// channelsFromProto maps the requested channels back to the domain enum.
// Unknown channel values are silently dropped; the repo treats nil/empty
// as "all channels".
func channelsFromProto(chs []v1.StreamChannel) []domain.StreamChannel {
	if len(chs) == 0 {
		return nil
	}
	out := make([]domain.StreamChannel, 0, len(chs))
	for _, c := range chs {
		if mapped, ok := streamChannelFromProto(c); ok {
			out = append(out, mapped)
		}
	}
	return out
}

func streamChannelFromProto(c v1.StreamChannel) (domain.StreamChannel, bool) {
	switch c {
	case v1.StreamChannel_STREAM_CHANNEL_LATITUDE:
		return domain.StreamChannelLatitude, true
	case v1.StreamChannel_STREAM_CHANNEL_LONGITUDE:
		return domain.StreamChannelLongitude, true
	case v1.StreamChannel_STREAM_CHANNEL_ALTITUDE:
		return domain.StreamChannelAltitude, true
	case v1.StreamChannel_STREAM_CHANNEL_DISTANCE:
		return domain.StreamChannelDistance, true
	case v1.StreamChannel_STREAM_CHANNEL_SPEED:
		return domain.StreamChannelSpeed, true
	case v1.StreamChannel_STREAM_CHANNEL_HEART_RATE:
		return domain.StreamChannelHeartRate, true
	case v1.StreamChannel_STREAM_CHANNEL_POWER:
		return domain.StreamChannelPower, true
	case v1.StreamChannel_STREAM_CHANNEL_CADENCE:
		return domain.StreamChannelCadence, true
	case v1.StreamChannel_STREAM_CHANNEL_TEMPERATURE:
		return domain.StreamChannelTemperature, true
	case v1.StreamChannel_STREAM_CHANNEL_GRADE:
		return domain.StreamChannelGrade, true
	}
	return "", false
}
