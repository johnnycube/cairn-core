// Package export renders a Cairn activity (its merged summary + stream) into
// the common interchange formats. The input is domain types only — the same
// merged stream the UI shows — so an export is independent of the original
// provider or file format the activity was imported from.
//
// GPX and TCX are hand-built XML (text, no dependency). FIT (binary) is a
// separate follow-on that needs an encoder.
package export

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// xmlEscape escapes the five XML predefined entities for element text /
// attribute values.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func esc(s string) string { return xmlEscaper.Replace(s) }

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// activityName picks a human title for the file, falling back to the type.
func activityName(a domain.Activity) string {
	if strings.TrimSpace(a.Title) != "" {
		return a.Title
	}
	if a.Discipline != domain.DisciplineNone && string(a.Discipline) != "" {
		return string(a.Discipline) + " activity"
	}
	return string(a.Type) + " activity"
}

// GPX renders the activity's GPS track as a GPX 1.1 document with the Garmin
// TrackPointExtension namespace for heart-rate / cadence / temperature, and a
// <power> element for power. Samples without a lat/lon are skipped (GPX track
// points require a position).
func GPX(a domain.Activity, s domain.Stream) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<gpx version="1.1" creator="Cairn" ` +
		`xmlns="http://www.topografix.com/GPX/1/1" ` +
		`xmlns:gpxtpx="http://www.garmin.com/xmlschemas/TrackPointExtension/v1">` + "\n")

	b.WriteString("  <metadata><name>" + esc(activityName(a)) + "</name>")
	if !a.StartTime.IsZero() {
		b.WriteString("<time>" + ts(a.StartTime) + "</time>")
	}
	b.WriteString("</metadata>\n")

	b.WriteString("  <trk>\n    <name>" + esc(activityName(a)) + "</name>\n")
	if t := string(a.Type); t != "" {
		b.WriteString("    <type>" + esc(t) + "</type>\n")
	}
	b.WriteString("    <trkseg>\n")

	for i := range s.Samples {
		p := &s.Samples[i]
		if p.Latitude == nil || p.Longitude == nil {
			continue
		}
		b.WriteString(`      <trkpt lat="` + f(*p.Latitude) + `" lon="` + f(*p.Longitude) + `">`)
		if p.AltitudeM != nil {
			b.WriteString("<ele>" + f(*p.AltitudeM) + "</ele>")
		}
		if !p.Timestamp.IsZero() {
			b.WriteString("<time>" + ts(p.Timestamp) + "</time>")
		}
		// Extensions: hr / cad / atemp (Garmin TPX) + power.
		if p.HeartRateBpm != nil || p.Cadence != nil || p.TemperatureC != nil || p.PowerW != nil {
			b.WriteString("<extensions>")
			if p.PowerW != nil {
				b.WriteString("<power>" + strconv.Itoa(int(*p.PowerW)) + "</power>")
			}
			if p.HeartRateBpm != nil || p.Cadence != nil || p.TemperatureC != nil {
				b.WriteString("<gpxtpx:TrackPointExtension>")
				if p.HeartRateBpm != nil {
					b.WriteString("<gpxtpx:hr>" + strconv.Itoa(int(*p.HeartRateBpm)) + "</gpxtpx:hr>")
				}
				if p.Cadence != nil {
					b.WriteString("<gpxtpx:cad>" + strconv.Itoa(int(*p.Cadence)) + "</gpxtpx:cad>")
				}
				if p.TemperatureC != nil {
					b.WriteString("<gpxtpx:atemp>" + f(float64(*p.TemperatureC)) + "</gpxtpx:atemp>")
				}
				b.WriteString("</gpxtpx:TrackPointExtension>")
			}
			b.WriteString("</extensions>")
		}
		b.WriteString("</trkpt>\n")
	}

	b.WriteString("    </trkseg>\n  </trk>\n</gpx>\n")
	return []byte(b.String())
}

// TCX renders the activity as a Training Center Database document. Unlike GPX,
// TCX trackpoints carry HR / cadence / distance / altitude / power even without
// a GPS position, so a no-GPS indoor activity still exports meaningfully. A
// single Lap spans the whole activity (valid TCX; per-lap splitting is a
// follow-on).
func TCX(a domain.Activity, s domain.Stream) []byte {
	sport := tcxSport(a)
	start := a.StartTime
	if start.IsZero() && len(s.Samples) > 0 {
		start = s.Samples[0].Timestamp
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<TrainingCenterDatabase ` +
		`xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2" ` +
		`xmlns:ns3="http://www.garmin.com/xmlschemas/ActivityExtension/v2">` + "\n")
	b.WriteString("  <Activities>\n    <Activity Sport=\"" + sport + "\">\n")
	b.WriteString("      <Id>" + ts(start) + "</Id>\n")
	b.WriteString("      <Lap StartTime=\"" + ts(start) + "\">\n")

	if a.ElapsedDuration > 0 {
		b.WriteString("        <TotalTimeSeconds>" + f(a.ElapsedDuration.Seconds()) + "</TotalTimeSeconds>\n")
	}
	if a.Summary.DistanceM != nil {
		b.WriteString("        <DistanceMeters>" + f(*a.Summary.DistanceM) + "</DistanceMeters>\n")
	}
	if a.Summary.MaxSpeedMps != nil {
		b.WriteString("        <MaximumSpeed>" + f(*a.Summary.MaxSpeedMps) + "</MaximumSpeed>\n")
	}
	if a.Summary.AvgHeartRateBpm != nil {
		b.WriteString("        <AverageHeartRateBpm><Value>" + strconv.Itoa(int(*a.Summary.AvgHeartRateBpm)) + "</Value></AverageHeartRateBpm>\n")
	}
	b.WriteString("        <Intensity>Active</Intensity>\n")
	b.WriteString("        <TriggerMethod>Manual</TriggerMethod>\n")
	b.WriteString("        <Track>\n")

	for i := range s.Samples {
		p := &s.Samples[i]
		b.WriteString("          <Trackpoint>\n")
		if !p.Timestamp.IsZero() {
			b.WriteString("            <Time>" + ts(p.Timestamp) + "</Time>\n")
		}
		if p.Latitude != nil && p.Longitude != nil {
			b.WriteString("            <Position><LatitudeDegrees>" + f(*p.Latitude) +
				"</LatitudeDegrees><LongitudeDegrees>" + f(*p.Longitude) + "</LongitudeDegrees></Position>\n")
		}
		if p.AltitudeM != nil {
			b.WriteString("            <AltitudeMeters>" + f(*p.AltitudeM) + "</AltitudeMeters>\n")
		}
		if p.DistanceM != nil {
			b.WriteString("            <DistanceMeters>" + f(*p.DistanceM) + "</DistanceMeters>\n")
		}
		if p.HeartRateBpm != nil {
			b.WriteString("            <HeartRateBpm><Value>" + strconv.Itoa(int(*p.HeartRateBpm)) + "</Value></HeartRateBpm>\n")
		}
		if p.Cadence != nil {
			b.WriteString("            <Cadence>" + strconv.Itoa(int(*p.Cadence)) + "</Cadence>\n")
		}
		if p.SpeedMps != nil || p.PowerW != nil {
			b.WriteString("            <Extensions><ns3:TPX>")
			if p.SpeedMps != nil {
				b.WriteString("<ns3:Speed>" + f(*p.SpeedMps) + "</ns3:Speed>")
			}
			if p.PowerW != nil {
				b.WriteString("<ns3:Watts>" + strconv.Itoa(int(*p.PowerW)) + "</ns3:Watts>")
			}
			b.WriteString("</ns3:TPX></Extensions>\n")
		}
		b.WriteString("          </Trackpoint>\n")
	}

	b.WriteString("        </Track>\n      </Lap>\n")
	b.WriteString("      <Creator xsi:type=\"Device_t\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"><Name>Cairn</Name></Creator>\n")
	b.WriteString("    </Activity>\n  </Activities>\n</TrainingCenterDatabase>\n")
	return []byte(b.String())
}

// tcxSport maps a Cairn activity type to one of TCX's enumerated sports
// (Running / Biking / Other — the only values the schema allows).
func tcxSport(a domain.Activity) string {
	switch a.Type {
	case domain.ActivityTypeRun:
		return "Running"
	case domain.ActivityTypeRide:
		return "Biking"
	default:
		return "Other"
	}
}

// Filename builds a download filename like "2026-06-05-morning-ride.gpx".
func Filename(a domain.Activity, ext string) string {
	base := "activity"
	if !a.StartTime.IsZero() {
		base = a.StartTime.UTC().Format("2006-01-02")
	}
	name := strings.ToLower(activityName(a))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ' || r == '-' || r == '_':
			return '-'
		default:
			return -1
		}
	}, name)
	name = strings.Trim(name, "-")
	if name != "" {
		base = base + "-" + name
	}
	return fmt.Sprintf("%s.%s", base, ext)
}
