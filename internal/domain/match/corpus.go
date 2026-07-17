// Package match is the fuzzy matching engine that clusters activity source
// records into logical activities (scoring, union-find, confidence bands), plus
// the labeled fixture corpus it's tested against.
package match

import "time"

// SourceFeatures is the blocking/scoring view of one source record. StartUTC is
// always UTC; StartLat/StartLng are nil for indoor/no-GPS sources (absent GPS is
// not evidence of "different").
type SourceFeatures struct {
	Provider   string
	SportClass string
	StartUTC   time.Time
	DistanceM  float64 // 0 when none
	MovingS    int64
	ElapsedS   int64
	StartLat   *float64
	StartLng   *float64
}

// Fixture is one labeled pair the matcher should resolve.
type Fixture struct {
	Name  string
	A, B  SourceFeatures
	Label string // one of the Label* constants
	Note  string
}

// Ground-truth labels. IndoorNoGPS is a "same" pair with no GPS on either side.
const (
	LabelSame        = "same"
	LabelDifferent   = "different"
	LabelIndoorNoGPS = "indoor_nogps"
)

// ValidLabels is the closed set of acceptable Fixture.Label values.
var ValidLabels = map[string]struct{}{
	LabelSame:        {},
	LabelDifferent:   {},
	LabelIndoorNoGPS: {},
}

func ll(v float64) *float64 { return &v }

// Corpus returns the labeled fixture corpus. Synthetic — augment with real
// exports via scripts/extract-match-corpus.sh + calibration_test.go.
func Corpus() []Fixture {
	// Shared GPS start reused by outdoor pairs.
	const baseLat, baseLng = 52.5170, 13.3889

	return []Fixture{
		// --- SAME: Garmin↔Strava auto-push of one ride -------------------
		// Strava auto-import from a connected Garmin account: start within a
		// couple of seconds, distance within ~0.3%, duration within ~1%,
		// same GPS start. The canonical free ground-truth "same".
		{
			Name: "garmin_strava_autopush_ride",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
				DistanceM: 30050, MovingS: 3360, ElapsedS: 3600,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 1, 9, 0, 2, 0, time.UTC), // +2s
				DistanceM: 30000, MovingS: 3300, ElapsedS: 3600,        // ~0.17% dist, ~1.8% moving
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			Label: LabelSame,
			Note:  "auto-push duplicate; start +2s, distance ~0.17%, same GPS start",
		},

		// --- DIFFERENT: two real rides the same day, ~3h apart -----------
		{
			Name: "two_rides_same_day_3h_apart",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 2, 7, 0, 0, 0, time.UTC),
				DistanceM: 25000, MovingS: 3000, ElapsedS: 3200,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "garmin", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC), // +3h
				DistanceM: 25200, MovingS: 3050, ElapsedS: 3250,
				StartLat: ll(52.4900), StartLng: ll(13.4200), // different start
			},
			Label: LabelDifferent,
			Note:  "morning vs late-morning ride; ~3h gap, different start point",
		},

		// --- DIFFERENT: treadmill run (no GPS) vs outdoor run ------------
		// Same sport class, similar distance, but one is indoor (no GPS) and
		// they are ~2h apart — genuinely different sessions.
		{
			Name: "treadmill_vs_outdoor_run",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 3, 6, 30, 0, 0, time.UTC),
				DistanceM: 10000, MovingS: 3000, ElapsedS: 3050,
				StartLat: nil, StartLng: nil, // treadmill, no GPS
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 3, 8, 30, 0, 0, time.UTC), // +2h
				DistanceM: 10100, MovingS: 3010, ElapsedS: 3100,
				StartLat: ll(baseLat), StartLng: ll(baseLng), // outdoor
			},
			Label: LabelDifferent,
			Note:  "indoor_nogps treadmill vs outdoor run, ~2h apart; absent GPS must not imply 'same'",
		},

		// --- SAME: near-midnight UTC-boundary pair -----------------------
		// One record lands at 23:59:58 UTC, its auto-push counterpart at
		// 00:00:01 UTC the next day. A day-bucketing matcher MUST use a
		// margin so this pair lands in the same bucket and clusters.
		{
			Name: "near_midnight_utc_boundary",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 4, 23, 59, 58, 0, time.UTC),
				DistanceM: 18000, MovingS: 2400, ElapsedS: 2500,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 5, 0, 0, 1, 0, time.UTC), // +3s, next UTC day
				DistanceM: 18020, MovingS: 2410, ElapsedS: 2500,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			Label: LabelSame,
			Note:  "crosses the UTC day boundary by 3s; day-bucket margin must keep them together",
		},

		// --- SAME: cross-timezone, identical UTC instant -----------------
		// Both records describe the SAME instant in UTC. If the matcher ever
		// compared local wall-clock times they'd look hours apart; in UTC
		// they're identical. (Provider strings differ; sport + metrics agree.)
		{
			Name: "cross_timezone_same_utc_instant",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 6, 16, 0, 0, 0, time.UTC), // 09:00 local (UTC-7)
				DistanceM: 8000, MovingS: 2400, ElapsedS: 2450,
				StartLat: ll(37.7749), StartLng: ll(-122.4194), // San Francisco
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 6, 16, 0, 1, 0, time.UTC), // same instant +1s
				DistanceM: 8005, MovingS: 2402, ElapsedS: 2450,
				StartLat: ll(37.7749), StartLng: ll(-122.4194),
			},
			Label: LabelSame,
			Note:  "identical UTC instant; local clocks would mislead — must compare in UTC",
		},

		// --- INDOOR_NOGPS: trainer ride duplicated across providers ------
		// A virtual/trainer ride (Zwift-style) pushed to two providers. No
		// GPS on either side; the matcher must cluster on time + distance +
		// sport alone, with NO coordinate tiebreaker available.
		{
			Name: "indoor_trainer_ride_two_providers",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "VirtualRide",
				StartUTC:  time.Date(2026, 5, 7, 18, 0, 0, 0, time.UTC),
				DistanceM: 40000, MovingS: 3600, ElapsedS: 3650,
				StartLat: nil, StartLng: nil,
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "VirtualRide",
				StartUTC:  time.Date(2026, 5, 7, 18, 0, 3, 0, time.UTC), // +3s
				DistanceM: 40050, MovingS: 3605, ElapsedS: 3650,
				StartLat: nil, StartLng: nil,
			},
			Label: LabelIndoorNoGPS,
			Note:  "no GPS on either side; cluster on time+distance+sport only",
		},

		// --- Extra coverage for breadth ----------------------------------

		// SAME: sport-class synonyms ("Run" vs "Running") of one upload.
		{
			Name: "sport_class_synonym_run_running",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Running",
				StartUTC:  time.Date(2026, 5, 8, 5, 45, 0, 0, time.UTC),
				DistanceM: 12000, MovingS: 3300, ElapsedS: 3360,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 8, 5, 45, 1, 0, time.UTC),
				DistanceM: 12010, MovingS: 3305, ElapsedS: 3360,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			Label: LabelSame,
			Note:  "fuzzy sport-class compatibility: 'Running' ~ 'Run'",
		},

		// DIFFERENT: same start time but different sport (brick: bike then run
		// recorded with overlapping starts is rare, but a Ride and a Run that
		// happen to start at the same second are NOT the same activity).
		{
			Name: "same_start_different_sport",
			A: SourceFeatures{
				Provider: "garmin", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
				DistanceM: 30000, MovingS: 3600, ElapsedS: 3700,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Run",
				StartUTC:  time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
				DistanceM: 8000, MovingS: 2400, ElapsedS: 2450,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			Label: LabelDifferent,
			Note:  "identical start instant but incompatible sport classes — gating must split",
		},

		// DIFFERENT: same provider, same sport, far apart in time (a backfill
		// must never collapse two real sessions just because metrics rhyme).
		{
			Name: "same_provider_two_sessions_days_apart",
			A: SourceFeatures{
				Provider: "strava", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
				DistanceM: 30000, MovingS: 3600, ElapsedS: 3700,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			B: SourceFeatures{
				Provider: "strava", SportClass: "Ride",
				StartUTC:  time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC), // +2 days
				DistanceM: 30050, MovingS: 3610, ElapsedS: 3710,
				StartLat: ll(baseLat), StartLng: ll(baseLng),
			},
			Label: LabelDifferent,
			Note:  "two days apart; near-identical metrics must not cluster across the time gap",
		},
	}
}
