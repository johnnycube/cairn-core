package trainingload

import (
	"testing"
	"time"
)

func TestTruncateUTCDay(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"mid-day UTC", "2026-05-01T15:30:45Z", "2026-05-01T00:00:00Z"},
		{"exact midnight", "2026-05-01T00:00:00Z", "2026-05-01T00:00:00Z"},
		{"end of day UTC", "2026-05-01T23:59:59Z", "2026-05-01T00:00:00Z"},
		// A non-UTC time near midnight: the function converts to UTC FIRST
		// then truncates, so 23:59-07:00 lands in the next UTC day.
		{"non-UTC crosses midnight", "2026-05-01T23:59:59-07:00", "2026-05-02T00:00:00Z"},
		// Equivalent of 06:30 UTC: stays in 2026-05-02.
		{"non-UTC stays in day", "2026-05-02T01:30:00-05:00", "2026-05-02T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := time.Parse(time.RFC3339, c.in)
			if err != nil {
				t.Fatalf("parse input: %v", err)
			}
			got := truncateUTCDay(in).Format(time.RFC3339)
			if got != c.want {
				t.Errorf("truncateUTCDay(%s) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}
