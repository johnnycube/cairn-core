package connect

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

const (
	uuidA = "11111111-1111-1111-1111-111111111111"
	uuidB = "22222222-2222-2222-2222-222222222222"
	uuidC = "33333333-3333-3333-3333-333333333333"
	uuidD = "44444444-4444-4444-4444-444444444444"
)

func ptr[T any](v T) *T { return &v }

func TestActivityToProto(t *testing.T) {
	start := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	deleted := start.Add(48 * time.Hour)
	gear := domain.GearID(uuid.MustParse(uuidC))
	primary := domain.SourceID(uuid.MustParse(uuidD))
	tags := []string{"commute", "rain"}
	a := domain.Activity{
		ID:                    domain.ActivityID(uuid.MustParse(uuidA)),
		UserID:                domain.UserID(uuid.MustParse(uuidB)),
		Type:                  domain.ActivityTypeRide,
		Discipline:            domain.DisciplineRideGravel,
		IsCommute:             true,
		Title:                 "Morning",
		StartTime:             start,
		EndTime:               start.Add(time.Hour),
		ElapsedDuration:       time.Hour,
		MovingDuration:        50 * time.Minute,
		Tags:                  tags,
		Privacy:               domain.PrivacyFollowers,
		StartPlace:            "Berlin",
		StartLat:              ptr(52.5),
		PrimaryStreamSourceID: &primary,
		GearID:                &gear,
		DeletedAt:             &deleted,
		MergeProvenance:       domain.MergeProvenance{"title": primary},
		Summary:               domain.ActivitySummary{DistanceM: ptr(1234.5), AvgHeartRateBpm: ptr(140)},
	}
	src := domain.ActivitySource{ID: primary, Provider: "strava", Status: domain.SourceStatusActive}

	out := activityToProto(a, []domain.ActivitySource{src})

	if out.GetId() != uuidA || out.GetUserId() != uuidB || out.GetTitle() != "Morning" || !out.GetIsCommute() {
		t.Errorf("identity fields = %v", out)
	}
	if out.GetType() != v1.ActivityType_ACTIVITY_TYPE_RIDE || out.GetDiscipline() != v1.Discipline_DISCIPLINE_RIDE_GRAVEL ||
		out.GetPrivacy() != v1.Privacy_PRIVACY_FOLLOWERS {
		t.Errorf("enums = %v %v %v", out.GetType(), out.GetDiscipline(), out.GetPrivacy())
	}
	if !out.GetStartTime().AsTime().Equal(start) || out.GetElapsedDuration().AsDuration() != time.Hour ||
		out.GetMovingDuration().AsDuration() != 50*time.Minute {
		t.Errorf("times = %v %v %v", out.GetStartTime(), out.GetElapsedDuration(), out.GetMovingDuration())
	}
	if out.GetPrimaryStreamSourceId() != uuidD || out.GetGearId() != uuidC || !out.GetDeletedAt().AsTime().Equal(deleted) {
		t.Errorf("optional ids = %q %q %v", out.GetPrimaryStreamSourceId(), out.GetGearId(), out.GetDeletedAt())
	}
	if out.GetStartPlace() != "Berlin" || out.GetStartLat() != 52.5 || out.StartLng != nil {
		t.Errorf("start location = %q %v %v", out.GetStartPlace(), out.StartLat, out.StartLng)
	}
	if out.GetMergeProvenance()["title"] != uuidD {
		t.Errorf("provenance = %v", out.GetMergeProvenance())
	}
	if out.GetSummary().GetDistanceM() != 1234.5 || out.GetSummary().GetAvgHeartRateBpm() != 140 {
		t.Errorf("summary = %v", out.GetSummary())
	}
	if len(out.GetSources()) != 1 || out.GetSources()[0].GetProvider() != "strava" {
		t.Errorf("sources = %v", out.GetSources())
	}

	// tags are copied, not aliased
	tags[0] = "changed"
	if out.GetTags()[0] != "commute" {
		t.Errorf("tags aliased: %v", out.GetTags())
	}

	bare := activityToProto(domain.Activity{}, nil)
	if bare.GetSources() != nil || bare.GetGearId() != "" || bare.GetPrimaryStreamSourceId() != "" ||
		bare.DeletedAt != nil || bare.GetMergeProvenance() != nil {
		t.Errorf("bare activity = %v", bare)
	}
	if bare.GetType() != v1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED || bare.GetPrivacy() != v1.Privacy_PRIVACY_UNSPECIFIED {
		t.Errorf("bare enums = %v %v", bare.GetType(), bare.GetPrivacy())
	}
}

func TestSourceToProto(t *testing.T) {
	acct := domain.ExternalAccountID(uuid.MustParse(uuidC))
	reimported := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := domain.ActivitySource{
		ID:                domain.SourceID(uuid.MustParse(uuidA)),
		ActivityID:        domain.ActivityID(uuid.MustParse(uuidB)),
		Provider:          "garmin",
		ExternalID:        "ext-1",
		ExternalAccountID: &acct,
		RawBlobID:         "blob",
		RawSizeBytes:      42,
		Status:            domain.SourceStatusDetached,
		ReimportStatus:    domain.ReimportStatusFailed,
		LastReimportedAt:  &reimported,
	}
	out := sourceToProto(s)
	if out.GetId() != uuidA || out.GetActivityId() != uuidB || out.GetExternalId() != "ext-1" ||
		out.GetRawBlobId() != "blob" || out.GetRawSizeBytes() != 42 || out.GetExternalAccountId() != uuidC {
		t.Errorf("fields = %v", out)
	}
	if out.GetStatus() != v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_DETACHED ||
		out.GetReimportStatus() != v1.ReimportStatus_REIMPORT_STATUS_FAILED {
		t.Errorf("statuses = %v %v", out.GetStatus(), out.GetReimportStatus())
	}
	if !out.GetLastReimportedAt().AsTime().Equal(reimported) {
		t.Errorf("last reimported = %v", out.GetLastReimportedAt())
	}

	bare := sourceToProto(domain.ActivitySource{})
	if bare.GetExternalAccountId() != "" || bare.LastReimportedAt != nil ||
		bare.GetStatus() != v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_UNSPECIFIED ||
		bare.GetReimportStatus() != v1.ReimportStatus_REIMPORT_STATUS_UNSPECIFIED {
		t.Errorf("bare source = %v", bare)
	}
}

func TestSummaryToProto(t *testing.T) {
	s := domain.ActivitySummary{
		DistanceM:        ptr(10.0),
		TSS:              ptr(55.5),
		AvgHeartRateBpm:  ptr(150),
		MaxPowerW:        ptr(math.MaxInt32 + 10),
		NormalizedPowerW: ptr(math.MinInt32 - 10),
		TotalStrokes:     ptr(0),
	}
	out := summaryToProto(s)
	if out.GetDistanceM() != 10 || out.GetTss() != 55.5 || out.GetAvgHeartRateBpm() != 150 {
		t.Errorf("summary = %v", out)
	}
	if out.GetMaxPowerW() != math.MaxInt32 || out.GetNormalizedPowerW() != math.MinInt32 {
		t.Errorf("clamping = %d %d", out.GetMaxPowerW(), out.GetNormalizedPowerW())
	}
	if out.TotalStrokes == nil || *out.TotalStrokes != 0 {
		t.Errorf("explicit zero lost: %v", out.TotalStrokes)
	}
	if out.MaxHeartRateBpm != nil || out.AvgPowerW != nil || out.ElevationGainM != nil {
		t.Errorf("unset fields populated: %v", out)
	}
}

func TestConnectClampInt32(t *testing.T) {
	tests := []struct {
		in   int
		want int32
	}{
		{0, 0},
		{-5, -5},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
		{math.MinInt32 - 1, math.MinInt32},
		{math.MaxInt64, math.MaxInt32},
	}
	for _, tt := range tests {
		if got := connectClampInt32(tt.in); got != tt.want {
			t.Errorf("connectClampInt32(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
	if intPtrToInt32(nil) != nil {
		t.Error("intPtrToInt32(nil) != nil")
	}
}

func TestActivityTypeRoundTrip(t *testing.T) {
	seen := map[v1.ActivityType]bool{}
	for _, dt := range domain.AllActivityTypes {
		p := activityTypeToProto(dt)
		if p == v1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
			t.Errorf("%q has no proto mapping", dt)
			continue
		}
		if seen[p] {
			t.Errorf("%q maps to duplicate %v", dt, p)
		}
		seen[p] = true
		back, ok := activityTypeFromProto(p)
		if !ok || back != dt {
			t.Errorf("round trip %q -> %v -> %q, %v", dt, p, back, ok)
		}
	}
	for num := range v1.ActivityType_name {
		p := v1.ActivityType(num)
		_, ok := activityTypeFromProto(p)
		if ok != (p != v1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED) {
			t.Errorf("activityTypeFromProto(%v) ok = %v", p, ok)
		}
	}
	if activityTypeToProto("unknown") != v1.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		t.Error("unknown type mapped")
	}
	if _, ok := activityTypeFromProto(999); ok {
		t.Error("out-of-range proto type accepted")
	}
}

func TestDisciplineRoundTrip(t *testing.T) {
	for num := range v1.Discipline_name {
		p := v1.Discipline(num)
		d, ok := disciplineFromProto(p)
		if p == v1.Discipline_DISCIPLINE_UNSPECIFIED {
			if ok || d != domain.DisciplineNone {
				t.Errorf("UNSPECIFIED -> %q, %v", d, ok)
			}
			continue
		}
		if !ok || d == domain.DisciplineNone {
			t.Errorf("disciplineFromProto(%v) = %q, %v", p, d, ok)
			continue
		}
		if back := disciplineToProto(d); back != p {
			t.Errorf("round trip %v -> %q -> %v", p, d, back)
		}
	}
	if disciplineToProto(domain.DisciplineNone) != v1.Discipline_DISCIPLINE_UNSPECIFIED {
		t.Error("DisciplineNone mapped")
	}
}

func TestPrivacyRoundTrip(t *testing.T) {
	for _, p := range []domain.ActivityPrivacy{domain.PrivacyPrivate, domain.PrivacyFollowers, domain.PrivacyPublic} {
		pp := privacyToProto(p)
		back, ok := privacyFromProto(pp)
		if pp == v1.Privacy_PRIVACY_UNSPECIFIED || !ok || back != p {
			t.Errorf("round trip %q -> %v -> %q, %v", p, pp, back, ok)
		}
	}
	if privacyToProto("secret") != v1.Privacy_PRIVACY_UNSPECIFIED {
		t.Error("unknown privacy mapped")
	}
	if _, ok := privacyFromProto(v1.Privacy_PRIVACY_UNSPECIFIED); ok {
		t.Error("UNSPECIFIED privacy accepted")
	}
}

func TestStatusEnumsToProto(t *testing.T) {
	statuses := map[domain.SourceStatus]v1.ActivitySourceStatus{
		domain.SourceStatusActive:             v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ACTIVE,
		domain.SourceStatusOrphaned:           v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ORPHANED,
		domain.SourceStatusAccountUnavailable: v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_ACCOUNT_UNAVAILABLE,
		domain.SourceStatusDetached:           v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_DETACHED,
		"bogus":                               v1.ActivitySourceStatus_ACTIVITY_SOURCE_STATUS_UNSPECIFIED,
	}
	for in, want := range statuses {
		if got := sourceStatusToProto(in); got != want {
			t.Errorf("sourceStatusToProto(%q) = %v, want %v", in, got, want)
		}
	}
	reimports := map[domain.ReimportStatus]v1.ReimportStatus{
		domain.ReimportStatusCurrent:         v1.ReimportStatus_REIMPORT_STATUS_CURRENT,
		domain.ReimportStatusUpdateAvailable: v1.ReimportStatus_REIMPORT_STATUS_UPDATE_AVAILABLE,
		domain.ReimportStatusUpdating:        v1.ReimportStatus_REIMPORT_STATUS_UPDATING,
		domain.ReimportStatusFailed:          v1.ReimportStatus_REIMPORT_STATUS_FAILED,
		"bogus":                              v1.ReimportStatus_REIMPORT_STATUS_UNSPECIFIED,
	}
	for in, want := range reimports {
		if got := reimportStatusToProto(in); got != want {
			t.Errorf("reimportStatusToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStreamToProtoEmpty(t *testing.T) {
	out := streamToProto(domain.Stream{
		ActivitySourceID: domain.SourceID(uuid.MustParse(uuidA)),
		Resolution:       domain.StreamResolution5s,
	})
	if out.GetActivitySourceId() != uuidA || out.GetSampleCount() != 0 || out.GetResolutionHz() != 0.2 {
		t.Errorf("header = %v", out)
	}
	if out.GetChannels() != nil || out.GetTimeS() != nil {
		t.Errorf("empty stream has columns: %v", out)
	}
}

func TestStreamToProto(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	samples := []domain.StreamSample{
		{Timestamp: t0, HeartRateBpm: ptr[int16](120), Grade: ptr[float32](1.5)},
		{Timestamp: t0.Add(time.Second), Latitude: ptr(52.0), Longitude: ptr(13.0), HeartRateBpm: ptr[int16](121)},
		{Timestamp: t0.Add(2500 * time.Millisecond), Latitude: ptr(52.1), PowerW: ptr[int16](250), TemperatureC: ptr[float32](20)},
	}
	out := streamToProto(domain.Stream{Resolution: domain.StreamResolutionRaw, Samples: samples})

	if out.GetSampleCount() != 3 || out.GetResolutionHz() != 1 {
		t.Errorf("header = %d %v", out.GetSampleCount(), out.GetResolutionHz())
	}
	if !slices.Equal(out.GetTimeS(), []float64{0, 1, 2.5}) {
		t.Errorf("time = %v", out.GetTimeS())
	}
	wantCh := []v1.StreamChannel{
		v1.StreamChannel_STREAM_CHANNEL_TIME,
		v1.StreamChannel_STREAM_CHANNEL_LATITUDE,
		v1.StreamChannel_STREAM_CHANNEL_LONGITUDE,
		v1.StreamChannel_STREAM_CHANNEL_HEART_RATE,
		v1.StreamChannel_STREAM_CHANNEL_POWER,
		v1.StreamChannel_STREAM_CHANNEL_TEMPERATURE,
		v1.StreamChannel_STREAM_CHANNEL_GRADE,
	}
	if !slices.Equal(out.GetChannels(), wantCh) {
		t.Errorf("channels = %v", out.GetChannels())
	}

	assertFloats(t, "lat", out.GetLatitude(), []float64{math.NaN(), 52.0, 52.1})
	assertFloats(t, "lon", out.GetLongitude(), []float64{math.NaN(), 13.0, math.NaN()})
	assertFloats(t, "temp", out.GetTemperatureC(), []float64{math.NaN(), math.NaN(), 20})
	assertFloats(t, "grade", out.GetGrade(), []float64{1.5, math.NaN(), math.NaN()})
	if !slices.Equal(out.GetHeartRateBpm(), []int32{120, 121, 0}) {
		t.Errorf("hr = %v", out.GetHeartRateBpm())
	}
	if !slices.Equal(out.GetPowerW(), []int32{0, 0, 250}) {
		t.Errorf("power = %v", out.GetPowerW())
	}
	if out.GetAltitudeM() != nil || out.GetDistanceM() != nil || out.GetSpeedMps() != nil || out.GetCadence() != nil {
		t.Errorf("absent channels populated: %v", out)
	}
}

func TestStreamToProtoAllCoreChannels(t *testing.T) {
	s := domain.StreamSample{
		Timestamp: time.Unix(0, 0), Latitude: ptr(1.0), Longitude: ptr(2.0), AltitudeM: ptr(3.0),
		DistanceM: ptr(4.0), SpeedMps: ptr(5.0), HeartRateBpm: ptr[int16](6), PowerW: ptr[int16](7),
		Cadence: ptr[int16](8), TemperatureC: ptr[float32](9), Grade: ptr[float32](10),
	}
	out := streamToProto(domain.Stream{Resolution: domain.StreamResolution30s, Samples: []domain.StreamSample{s}})
	if len(out.GetChannels()) != 11 {
		t.Errorf("channels = %v", out.GetChannels())
	}
	if out.GetAltitudeM()[0] != 3 || out.GetDistanceM()[0] != 4 || out.GetSpeedMps()[0] != 5 || out.GetCadence()[0] != 8 {
		t.Errorf("columns = %v", out)
	}
	if math.Abs(out.GetResolutionHz()-1.0/30.0) > 1e-12 {
		t.Errorf("resolution = %v", out.GetResolutionHz())
	}
}

func assertFloats(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
		return
	}
	for i := range want {
		if math.IsNaN(want[i]) != math.IsNaN(got[i]) || (!math.IsNaN(want[i]) && got[i] != want[i]) {
			t.Errorf("%s = %v, want %v", name, got, want)
			return
		}
	}
}

func TestResolutionHz(t *testing.T) {
	if resolutionHz("weird") != 0 || resolutionHz(domain.StreamResolutionRaw) != 1 {
		t.Error("resolutionHz mismatch")
	}
	if !math.IsNaN(nan()) {
		t.Error("nan() is not NaN")
	}
}

func TestBestEffortToProto(t *testing.T) {
	ts := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	b := domain.BestEffort{
		ID:               domain.BestEffortID(uuid.MustParse(uuidA)),
		ActivityID:       domain.ActivityID(uuid.MustParse(uuidB)),
		ActivitySourceID: domain.SourceID(uuid.MustParse(uuidC)),
		UserID:           domain.UserID(uuid.MustParse(uuidD)),
		ActivityType:     domain.ActivityTypeRun,
		Discipline:       domain.DisciplineRunTrail,
		Metric:           domain.BestEffortMetricPace,
		WindowKind:       domain.BestEffortWindowDistance,
		WindowValue:      5000,
		AchievedValue:    4.2,
		StartOffset:      90,
		DurationS:        1260.5,
		DistanceM:        ptr(5000.0),
		Timestamp:        ts,
	}
	out := bestEffortToProto(b)
	if out.GetId() != uuidA || out.GetActivityId() != uuidB || out.GetActivitySourceId() != uuidC || out.GetUserId() != uuidD {
		t.Errorf("ids = %v", out)
	}
	if out.GetActivityType() != v1.ActivityType_ACTIVITY_TYPE_RUN || out.GetDiscipline() != v1.Discipline_DISCIPLINE_RUN_TRAIL ||
		out.GetMetric() != v1.BestEffortMetric_BEST_EFFORT_METRIC_PACE ||
		out.GetWindowKind() != v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_DISTANCE {
		t.Errorf("enums = %v", out)
	}
	if out.GetWindowValue() != 5000 || out.GetAchievedValue() != 4.2 || out.GetStartOffset() != 90 ||
		out.GetDuration().AsDuration() != 1260500*time.Millisecond || !out.GetTs().AsTime().Equal(ts) || out.GetDistanceM() != 5000 {
		t.Errorf("values = %v", out)
	}

	bare := bestEffortToProto(domain.BestEffort{WindowKind: domain.BestEffortWindowDuration})
	if bare.DistanceM != nil || bare.GetWindowKind() != v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_DURATION ||
		bare.GetMetric() != v1.BestEffortMetric_BEST_EFFORT_METRIC_UNSPECIFIED {
		t.Errorf("bare = %v", bare)
	}
	if bestEffortWindowKindToProto("x") != v1.BestEffortWindowKind_BEST_EFFORT_WINDOW_KIND_UNSPECIFIED {
		t.Error("unknown window kind mapped")
	}
}

func TestBestEffortMetricRoundTrip(t *testing.T) {
	for _, m := range []domain.BestEffortMetric{
		domain.BestEffortMetricPace, domain.BestEffortMetricSpeed, domain.BestEffortMetricPower,
		domain.BestEffortMetricHeartRate, domain.BestEffortMetricVAM,
	} {
		p := bestEffortMetricToProto(m)
		back, ok := bestEffortMetricFromProto(p)
		if p == v1.BestEffortMetric_BEST_EFFORT_METRIC_UNSPECIFIED || !ok || back != m {
			t.Errorf("round trip %q -> %v -> %q, %v", m, p, back, ok)
		}
	}
	if _, ok := bestEffortMetricFromProto(v1.BestEffortMetric_BEST_EFFORT_METRIC_UNSPECIFIED); ok {
		t.Error("UNSPECIFIED metric accepted")
	}
}

func TestNotificationToProto(t *testing.T) {
	created := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	readAt := created.Add(time.Minute)
	act := domain.ActivityID(uuid.MustParse(uuidB))
	seg := domain.SegmentID(uuid.MustParse(uuidC))
	acct := domain.ExternalAccountID(uuid.MustParse(uuidD))
	n := domain.Notification{
		ID:                domain.NotificationID(uuid.MustParse(uuidA)),
		Type:              domain.NotificationTypeSegmentPersonalRecord,
		Severity:          domain.NotificationSeverityWarn,
		TitleI18nKey:      "t",
		BodyI18nKey:       "b",
		I18nParams:        map[string]string{"k": "v"},
		ActivityID:        &act,
		SegmentID:         &seg,
		ExternalAccountID: &acct,
		WorkerName:        "strava",
		DedupKey:          "d",
		CoalesceCount:     3,
		Read:              true,
		ReadAt:            &readAt,
		CreatedAt:         created,
	}
	out := notificationToProto(n)
	if out.GetId() != uuidA || out.GetActivityId() != uuidB || out.GetSegmentId() != uuidC ||
		out.GetExternalAccountId() != uuidD || out.GetWorkerName() != "strava" {
		t.Errorf("refs = %v", out)
	}
	if out.GetType() != v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACHIEVEMENT_SEGMENT_PR ||
		out.GetSeverity() != v1.NotificationSeverity_NOTIFICATION_SEVERITY_WARN {
		t.Errorf("enums = %v %v", out.GetType(), out.GetSeverity())
	}
	if out.GetTitleI18NKey() != "t" || out.GetBodyI18NKey() != "b" || out.GetI18NParams()["k"] != "v" ||
		out.GetDedupKey() != "d" || out.GetCoalesceCount() != 3 || !out.GetRead() ||
		!out.GetReadAt().AsTime().Equal(readAt) || !out.GetCreatedAt().AsTime().Equal(created) {
		t.Errorf("fields = %v", out)
	}

	bare := notificationToProto(domain.Notification{})
	if bare.ActivityId != nil || bare.SegmentId != nil || bare.ExternalAccountId != nil ||
		bare.WorkerName != nil || bare.ReadAt != nil {
		t.Errorf("bare optional fields set: %v", bare)
	}
	if bare.GetType() != v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_UNSPECIFIED ||
		bare.GetSeverity() != v1.NotificationSeverity_NOTIFICATION_SEVERITY_UNSPECIFIED {
		t.Errorf("bare enums = %v %v", bare.GetType(), bare.GetSeverity())
	}
}

func TestNotificationEnumsToProto(t *testing.T) {
	types := map[domain.NotificationType]v1.NotificationEventType{
		domain.NotificationTypeSegmentPersonalRecord:        v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACHIEVEMENT_SEGMENT_PR,
		domain.NotificationTypeSegmentInstanceRecord:        v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACHIEVEMENT_SEGMENT_KOM,
		domain.NotificationTypeActivityImported:             v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_BULK_IMPORT_COMPLETED,
		domain.NotificationTypeActivityReimported:           v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_AUTO_REIMPORT_COMPLETED,
		domain.NotificationTypeWorkerOffline:                v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_SYNC_WORKER_OFFLINE_FOR_ACCOUNT,
		domain.NotificationTypeExternalAccountRefreshFailed: v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_ACCOUNT_EXTERNAL_AUTH_INVALID,
		domain.NotificationTypeUnspecified:                  v1.NotificationEventType_NOTIFICATION_EVENT_TYPE_UNSPECIFIED,
	}
	for in, want := range types {
		if got := notificationTypeToProto(in); got != want {
			t.Errorf("notificationTypeToProto(%v) = %v, want %v", in, got, want)
		}
	}
	sev := map[domain.NotificationSeverity]v1.NotificationSeverity{
		domain.NotificationSeverityInfo:  v1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
		domain.NotificationSeverityWarn:  v1.NotificationSeverity_NOTIFICATION_SEVERITY_WARN,
		domain.NotificationSeverityError: v1.NotificationSeverity_NOTIFICATION_SEVERITY_ERROR,
		"fatal":                          v1.NotificationSeverity_NOTIFICATION_SEVERITY_UNSPECIFIED,
	}
	for in, want := range sev {
		if got := notificationSeverityToProto(in); got != want {
			t.Errorf("notificationSeverityToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestChannelsFromProto(t *testing.T) {
	if channelsFromProto(nil) != nil {
		t.Error("nil input should give nil")
	}
	got := channelsFromProto([]v1.StreamChannel{
		v1.StreamChannel_STREAM_CHANNEL_TIME,
		v1.StreamChannel_STREAM_CHANNEL_LATITUDE,
		v1.StreamChannel_STREAM_CHANNEL_LONGITUDE,
		v1.StreamChannel_STREAM_CHANNEL_ALTITUDE,
		v1.StreamChannel_STREAM_CHANNEL_DISTANCE,
		v1.StreamChannel_STREAM_CHANNEL_SPEED,
		v1.StreamChannel_STREAM_CHANNEL_HEART_RATE,
		v1.StreamChannel_STREAM_CHANNEL_POWER,
		v1.StreamChannel_STREAM_CHANNEL_CADENCE,
		v1.StreamChannel_STREAM_CHANNEL_TEMPERATURE,
		v1.StreamChannel_STREAM_CHANNEL_GRADE,
		v1.StreamChannel_STREAM_CHANNEL_UNSPECIFIED,
	})
	want := domain.AllStreamChannels[:10]
	if !slices.Equal(got, want) {
		t.Errorf("channels = %v, want %v", got, want)
	}
}

func TestChannelsFromProtoAllChannels(t *testing.T) {
	var all []v1.StreamChannel
	for n := range v1.StreamChannel_name {
		if c := v1.StreamChannel(n); c != v1.StreamChannel_STREAM_CHANNEL_UNSPECIFIED && c != v1.StreamChannel_STREAM_CHANNEL_TIME {
			all = append(all, c)
		}
	}
	slices.Sort(all)
	if got := channelsFromProto(all); !slices.Equal(got, domain.AllStreamChannels) {
		t.Errorf("channels = %v, want %v", got, domain.AllStreamChannels)
	}
}

func TestChannelsFromProtoSubsets(t *testing.T) {
	timeOnly := channelsFromProto([]v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_TIME})
	if timeOnly == nil || len(timeOnly) != 0 {
		t.Errorf("TIME only = %#v, want empty non-nil", timeOnly)
	}
	adv := channelsFromProto([]v1.StreamChannel{
		v1.StreamChannel_STREAM_CHANNEL_LEFT_RIGHT_BALANCE,
		v1.StreamChannel_STREAM_CHANNEL_CORE_TEMPERATURE,
	})
	if !slices.Equal(adv, []domain.StreamChannel{domain.StreamChannelLeftRightBalance, domain.StreamChannelCoreTemperature}) {
		t.Errorf("advanced = %v", adv)
	}
	if got := channelsFromProto([]v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_UNSPECIFIED}); got != nil {
		t.Errorf("UNSPECIFIED only = %v, want nil", got)
	}
}

func TestStreamToProtoKeepChannels(t *testing.T) {
	hr, lat := int16(120), 47.1
	s := domain.Stream{Samples: []domain.StreamSample{
		{Timestamp: time.Unix(0, 0), HeartRateBpm: &hr, Latitude: &lat},
		{Timestamp: time.Unix(1, 0), HeartRateBpm: &hr, Latitude: &lat},
	}}
	got := streamToProto(s.KeepChannels(channelsFromProto([]v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_TIME})))
	if !slices.Equal(got.GetChannels(), []v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_TIME}) ||
		len(got.GetTimeS()) != 2 || got.GetHeartRateBpm() != nil || got.GetLatitude() != nil {
		t.Errorf("TIME only stream = %v", got)
	}
	got = streamToProto(s.KeepChannels(channelsFromProto([]v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_HEART_RATE})))
	if !slices.Equal(got.GetChannels(), []v1.StreamChannel{v1.StreamChannel_STREAM_CHANNEL_TIME, v1.StreamChannel_STREAM_CHANNEL_HEART_RATE}) {
		t.Errorf("HR only channels = %v", got.GetChannels())
	}
}
