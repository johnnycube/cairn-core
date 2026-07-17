package domain

import (
	"testing"
	"time"
)

func at(h, m int) time.Time {
	// A Wednesday in UTC.
	return time.Date(2026, 6, 3, h, m, 0, 0, time.UTC)
}

func TestQuietHours_Disabled(t *testing.T) {
	q := QuietHours{Enabled: false, StartMinute: 0, EndMinute: 1440, TZ: "UTC"}
	if q.Suppresses(at(3, 0)) {
		t.Fatal("disabled quiet hours must never suppress")
	}
}

func TestQuietHours_WrapMidnight(t *testing.T) {
	// 22:00 → 07:00 UTC
	q := QuietHours{Enabled: true, StartMinute: 22 * 60, EndMinute: 7 * 60, TZ: "UTC"}
	if !q.Suppresses(at(23, 0)) {
		t.Error("23:00 should be inside 22:00→07:00")
	}
	if !q.Suppresses(at(2, 0)) {
		t.Error("02:00 should be inside 22:00→07:00")
	}
	if q.Suppresses(at(12, 0)) {
		t.Error("12:00 should be outside 22:00→07:00")
	}
}

func TestQuietHours_Timezone(t *testing.T) {
	// 22:00→07:00 in Europe/Berlin (UTC+2 in June). 21:00 UTC == 23:00 Berlin → inside.
	q := QuietHours{Enabled: true, StartMinute: 22 * 60, EndMinute: 7 * 60, TZ: "Europe/Berlin"}
	if !q.Suppresses(at(21, 0)) {
		t.Error("21:00 UTC == 23:00 Berlin should be inside the Berlin window")
	}
	// 19:00 UTC == 21:00 Berlin → outside
	if q.Suppresses(at(19, 0)) {
		t.Error("19:00 UTC == 21:00 Berlin should be outside")
	}
}

func TestQuietHours_DaysOfWeek(t *testing.T) {
	// only Wednesday (3). at() is a Wednesday.
	q := QuietHours{Enabled: true, StartMinute: 0, EndMinute: 1440, DaysOfWeek: []int{3}, TZ: "UTC"}
	if !q.Suppresses(at(10, 0)) {
		t.Error("Wednesday should match days_of_week [3]")
	}
	q.DaysOfWeek = []int{1} // Monday only
	if q.Suppresses(at(10, 0)) {
		t.Error("Wednesday should not match days_of_week [1]")
	}
}
