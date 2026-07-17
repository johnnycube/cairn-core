package domain

import (
	"math"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func entry(k AthleteMetricKey, d string, v float64) AthleteMetricEntry {
	return AthleteMetricEntry{Key: k, EffectiveDate: day(d), Value: v}
}

func TestAthleteProfileValueAt(t *testing.T) {
	p := NewAthleteProfile([]AthleteMetricEntry{
		entry(AthleteFTPWatts, "2024-01-01", 200),
		entry(AthleteFTPWatts, "2024-07-01", 260), // 182 days later
	})

	cases := []struct {
		name string
		at   string
		want float64
	}{
		{"clamp before range", "2023-06-01", 200},
		{"exact lower", "2024-01-01", 200},
		{"exact upper", "2024-07-01", 260},
		{"clamp after range", "2025-01-01", 260},
		// midpoint: 2024-04-01 is 91 days into a 182-day span → halfway → 230
		{"interpolated midpoint", "2024-04-01", 230},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := p.ValueAt(AthleteFTPWatts, day(c.at))
			if !ok {
				t.Fatalf("ValueAt not ok")
			}
			if math.Abs(got-c.want) > 0.5 {
				t.Fatalf("ValueAt(%s) = %.2f, want %.2f", c.at, got, c.want)
			}
		})
	}
}

func TestAthleteProfileMissingKey(t *testing.T) {
	p := NewAthleteProfile(nil)
	if _, ok := p.ValueAt(AthleteFTPWatts, day("2024-01-01")); ok {
		t.Fatal("expected ok=false for empty profile")
	}
	// ThresholdsAt leaves fields zero so EstimateTSS uses defaults.
	th := p.ThresholdsAt(day("2024-01-01"))
	if th.FTPWatts != 0 || th.ThresholdHRBpm != 0 {
		t.Fatalf("expected zero thresholds, got %+v", th)
	}
}

func TestEstimateTSSUsesResolvedFTP(t *testing.T) {
	np := 200
	s := ActivitySummary{NormalizedPowerW: &np}
	// 1h at NP 200. With FTP 200 → IF 1.0 → TSS 100. With FTP 260 → IF 0.77 → ~59.
	tss200, _, ok := EstimateTSS(s, time.Hour, AthleteThresholds{FTPWatts: 200})
	if !ok || math.Abs(tss200-100) > 0.5 {
		t.Fatalf("FTP 200 → TSS %.2f, want 100", tss200)
	}
	tss260, _, _ := EstimateTSS(s, time.Hour, AthleteThresholds{FTPWatts: 260})
	if !(tss260 < tss200) {
		t.Fatalf("higher FTP should lower TSS: %.2f vs %.2f", tss260, tss200)
	}
}
