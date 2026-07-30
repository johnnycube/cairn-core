package export

import (
	"fmt"
	"strings"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// Options selects which parts of the activity end up in an exported file.
// The renderers emit per non-nil field, so exclusion works by stripping the
// data before rendering (Apply) rather than by branching in each format.
type Options struct {
	GPS         bool // latitude/longitude
	Altitude    bool
	Distance    bool // per-sample cumulative distance + summary distance
	Speed       bool // per-sample speed + summary max speed
	HeartRate   bool // per-sample HR + summary avg HR
	Power       bool
	Cadence     bool
	Temperature bool
	Title       bool // activity title in file metadata and filename
}

// AllOptions is the default: everything the formats can carry is included.
func AllOptions() Options {
	return Options{
		GPS: true, Altitude: true, Distance: true, Speed: true,
		HeartRate: true, Power: true, Cadence: true, Temperature: true,
		Title: true,
	}
}

// featureFields maps the API feature tokens (?exclude=..., options endpoint)
// to their Options field. Keep in sync with the manage-page checkbox list.
var featureFields = map[string]func(*Options) *bool{
	"gps":         func(o *Options) *bool { return &o.GPS },
	"altitude":    func(o *Options) *bool { return &o.Altitude },
	"distance":    func(o *Options) *bool { return &o.Distance },
	"speed":       func(o *Options) *bool { return &o.Speed },
	"heart_rate":  func(o *Options) *bool { return &o.HeartRate },
	"power":       func(o *Options) *bool { return &o.Power },
	"cadence":     func(o *Options) *bool { return &o.Cadence },
	"temperature": func(o *Options) *bool { return &o.Temperature },
	"title":       func(o *Options) *bool { return &o.Title },
}

// OptionsFromExclude builds Options from a comma-separated exclusion list.
// Empty input means AllOptions; an unknown token is an error.
func OptionsFromExclude(exclude string) (Options, error) {
	o := AllOptions()
	if strings.TrimSpace(exclude) == "" {
		return o, nil
	}
	for _, tok := range strings.Split(exclude, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		field, ok := featureFields[tok]
		if !ok {
			return o, fmt.Errorf("unknown export feature %q", tok)
		}
		*field(&o) = false
	}
	return o, nil
}

// Apply returns copies of the activity and stream with everything an excluded
// option covers removed. Summary fields follow their channel so e.g. a TCX
// lap doesn't leak the avg HR of an excluded HR stream.
func Apply(o Options, a domain.Activity, s domain.Stream) (domain.Activity, domain.Stream) {
	if !o.Title {
		a.Title = ""
	}
	if !o.Distance {
		a.Summary.DistanceM = nil
	}
	if !o.Speed {
		a.Summary.MaxSpeedMps = nil
	}
	if !o.HeartRate {
		a.Summary.AvgHeartRateBpm = nil
	}

	if o == AllOptions() {
		return a, s
	}
	samples := make([]domain.StreamSample, len(s.Samples))
	copy(samples, s.Samples)
	for i := range samples {
		p := &samples[i]
		if !o.GPS {
			p.Latitude, p.Longitude = nil, nil
		}
		if !o.Altitude {
			p.AltitudeM = nil
		}
		if !o.Distance {
			p.DistanceM = nil
		}
		if !o.Speed {
			p.SpeedMps = nil
		}
		if !o.HeartRate {
			p.HeartRateBpm = nil
		}
		if !o.Power {
			p.PowerW = nil
		}
		if !o.Cadence {
			p.Cadence = nil
		}
		if !o.Temperature {
			p.TemperatureC = nil
		}
	}
	s.Samples = samples
	return a, s
}

// Available reports, per feature token, whether the activity/stream actually
// carries that data — what the UI offers as checkable.
func Available(a domain.Activity, s domain.Stream) map[string]bool {
	av := map[string]bool{
		"gps": false, "altitude": false, "distance": false, "speed": false,
		"heart_rate": false, "power": false, "cadence": false, "temperature": false,
		"title": strings.TrimSpace(a.Title) != "",
	}
	for i := range s.Samples {
		p := &s.Samples[i]
		if p.Latitude != nil && p.Longitude != nil {
			av["gps"] = true
		}
		if p.AltitudeM != nil {
			av["altitude"] = true
		}
		if p.DistanceM != nil {
			av["distance"] = true
		}
		if p.SpeedMps != nil {
			av["speed"] = true
		}
		if p.HeartRateBpm != nil {
			av["heart_rate"] = true
		}
		if p.PowerW != nil {
			av["power"] = true
		}
		if p.Cadence != nil {
			av["cadence"] = true
		}
		if p.TemperatureC != nil {
			av["temperature"] = true
		}
	}
	return av
}
