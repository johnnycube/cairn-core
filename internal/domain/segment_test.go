package domain

import (
	"errors"
	"math"
	"testing"
)

func TestSegmentEnumsValid(t *testing.T) {
	for _, s := range []SegmentSource{SegmentSourceExternal, SegmentSourceNative} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if SegmentSource("strava").Valid() {
		t.Error("unknown source accepted")
	}
	for _, s := range []SegmentScope{SegmentScopePrivate, SegmentScopeInstance} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if SegmentScope("public").Valid() {
		t.Error("unknown scope accepted")
	}
	for _, c := range []ClimbCategory{
		ClimbCategoryNone, ClimbCategory4, ClimbCategory3,
		ClimbCategory2, ClimbCategory1, ClimbCategoryHorsCategorie,
	} {
		if !c.Valid() {
			t.Errorf("%q should be valid", c)
		}
	}
	if ClimbCategory("cat_5").Valid() || ClimbCategory("").Valid() {
		t.Error("unknown climb category accepted")
	}
}

func validNativeSegment() Segment {
	return Segment{
		Source:            SegmentSourceNative,
		OwnerUserID:       &UserID{},
		Scope:             SegmentScopeInstance,
		ActivityType:      ActivityTypeRide,
		Polyline:          "_p~iF~ps|U",
		PolylinePrecision: 5,
		DistanceM:         100,
	}
}

func validExternalSegment() Segment {
	ext := "123"
	return Segment{
		Source:            SegmentSourceExternal,
		ExternalID:        &ext,
		ExternalAccountID: &ExternalAccountID{},
		Scope:             SegmentScopePrivate,
		ActivityType:      ActivityTypeRun,
		Polyline:          "_p~iF~ps|U",
		PolylinePrecision: 6,
		DistanceM:         1,
	}
}

func TestSegmentValidate(t *testing.T) {
	ext := "x"
	tests := []struct {
		name string
		mut  func(*Segment)
		base func() Segment
		want error
	}{
		{"native ok", func(*Segment) {}, validNativeSegment, nil},
		{"external ok", func(*Segment) {}, validExternalSegment, nil},
		{"bad source", func(s *Segment) { s.Source = "x" }, validNativeSegment, ErrSegmentInvalidSource},
		{"bad scope", func(s *Segment) { s.Scope = "x" }, validNativeSegment, ErrSegmentInvalidScope},
		{"bad activity type", func(s *Segment) { s.ActivityType = "x" }, validNativeSegment, ErrSegmentInvalidActivityType},
		{"external missing id", func(s *Segment) { s.ExternalID = nil }, validExternalSegment, ErrSegmentExternalMissingExternal},
		{"external missing account", func(s *Segment) { s.ExternalAccountID = nil }, validExternalSegment, ErrSegmentExternalMissingExternal},
		{"external with owner", func(s *Segment) { s.OwnerUserID = &UserID{} }, validExternalSegment, ErrSegmentExternalUnexpectedOwner},
		{"external instance scope", func(s *Segment) { s.Scope = SegmentScopeInstance }, validExternalSegment, ErrSegmentExternalScopeNotPrivate},
		{"native missing owner", func(s *Segment) { s.OwnerUserID = nil }, validNativeSegment, ErrSegmentNativeMissingOwner},
		{"native with external id", func(s *Segment) { s.ExternalID = &ext }, validNativeSegment, ErrSegmentNativeUnexpectedExternal},
		{"native with external account", func(s *Segment) { s.ExternalAccountID = &ExternalAccountID{} }, validNativeSegment, ErrSegmentNativeUnexpectedExternal},
		{"blank polyline", func(s *Segment) { s.Polyline = "  " }, validNativeSegment, ErrSegmentMissingPolyline},
		{"bad precision", func(s *Segment) { s.PolylinePrecision = 7 }, validNativeSegment, ErrSegmentInvalidPolylinePrecision},
		{"zero distance", func(s *Segment) { s.DistanceM = 0 }, validNativeSegment, ErrSegmentNonPositiveDistance},
		{"negative distance", func(s *Segment) { s.DistanceM = -1 }, validNativeSegment, ErrSegmentNonPositiveDistance},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.base()
			tt.mut(&s)
			if err := s.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func approxPoints(t *testing.T, got, want []GeoPoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d points %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if math.Abs(got[i].Lat-want[i].Lat) > 1e-9 || math.Abs(got[i].Lon-want[i].Lon) > 1e-9 {
			t.Fatalf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDecodePolyline(t *testing.T) {
	// Reference example from Google's polyline algorithm docs.
	googleExample := []GeoPoint{{38.5, -120.2}, {40.7, -120.95}, {43.252, -126.453}}

	tests := []struct {
		name      string
		encoded   string
		precision int
		want      []GeoPoint
		wantErr   bool
	}{
		{"google example", "_p~iF~ps|U_ulLnnqC_mqNvxq`@", 5, googleExample, false},
		{"precision 6 scales down", "_p~iF~ps|U", 6, []GeoPoint{{3.85, -12.02}}, false},
		{"empty", "", 5, nil, false},
		{"zero point", "??", 5, []GeoPoint{{0, 0}}, false},
		{"bad precision", "??", 4, nil, true},
		{"char below range", "  ", 5, nil, true},
		{"char above range", "\x7f?", 5, nil, true},
		{"truncated chunk", "_p~iF~ps|", 5, nil, true},
		{"lat without lng", "_p~iF", 5, nil, true},
		{"overflow shift", "~~~~~~~~~~~~~~", 5, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodePolyline(tt.encoded, tt.precision)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPolyline) {
					t.Fatalf("err = %v, want ErrInvalidPolyline", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			approxPoints(t, got, tt.want)
		})
	}
}

func TestSegmentDecodedPolyline(t *testing.T) {
	s := Segment{Polyline: "_p~iF~ps|U", PolylinePrecision: 5}
	got, err := s.DecodedPolyline()
	if err != nil {
		t.Fatal(err)
	}
	approxPoints(t, got, []GeoPoint{{38.5, -120.2}})
}

func TestHaversineMeters(t *testing.T) {
	tests := []struct {
		name string
		a, b GeoPoint
		want float64
		tol  float64
	}{
		{"same point", GeoPoint{48, 11}, GeoPoint{48, 11}, 0, 1e-9},
		{"one degree latitude", GeoPoint{0, 0}, GeoPoint{1, 0}, EarthRadiusMeters * math.Pi / 180, 1e-6},
		{"one degree lon at equator", GeoPoint{0, 0}, GeoPoint{0, 1}, EarthRadiusMeters * math.Pi / 180, 1e-6},
		{"antipodes", GeoPoint{0, 0}, GeoPoint{0, 180}, EarthRadiusMeters * math.Pi, 1e-3},
		{"berlin to munich", GeoPoint{52.52, 13.405}, GeoPoint{48.1351, 11.582}, 504_000, 2_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HaversineMeters(tt.a, tt.b)
			if math.Abs(got-tt.want) > tt.tol {
				t.Fatalf("got %f, want %f±%f", got, tt.want, tt.tol)
			}
			if back := HaversineMeters(tt.b, tt.a); math.Abs(back-got) > 1e-6 {
				t.Fatalf("not symmetric: %f vs %f", got, back)
			}
		})
	}
}

func TestEffectiveTolerances(t *testing.T) {
	base := DefaultMatchTolerances()
	if base != (MatchTolerances{CorridorM: 15, StartToleranceM: 25, EndToleranceM: 25}) {
		t.Fatalf("defaults = %+v", base)
	}

	var s Segment
	if got := s.EffectiveTolerances(base); got != base {
		t.Fatalf("no overrides: %+v", got)
	}

	c, st, en := 5.0, 6.0, 7.0
	s.MatchCorridorM = &c
	if got := s.EffectiveTolerances(base); got != (MatchTolerances{5, 25, 25}) {
		t.Fatalf("corridor override: %+v", got)
	}
	s.MatchStartToleranceM = &st
	s.MatchEndToleranceM = &en
	if got := s.EffectiveTolerances(base); got != (MatchTolerances{5, 6, 7}) {
		t.Fatalf("all overrides: %+v", got)
	}
}
