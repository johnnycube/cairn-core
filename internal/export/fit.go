package export

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tormoder/fit"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// FIT renders the activity as a binary FIT activity file using tormoder/fit.
// It writes a FileId + one Record per stream sample + a single Lap, Session and
// Activity message — the minimal message set a FIT activity file needs to be
// accepted by Garmin Connect / Strava. Scaled fields use the standard FIT
// profile scales (altitude ×5 −500, distance ×100, speed ×1000, time ×1000).
func FIT(a domain.Activity, s domain.Stream) ([]byte, error) {
	f, err := fit.NewFile(fit.FileTypeActivity, fit.NewHeader(fit.V20, true))
	if err != nil {
		return nil, fmt.Errorf("new fit file: %w", err)
	}

	start := a.StartTime
	if start.IsZero() && len(s.Samples) > 0 {
		start = s.Samples[0].Timestamp
	}
	end := a.EndTime
	if end.IsZero() && len(s.Samples) > 0 {
		end = s.Samples[len(s.Samples)-1].Timestamp
	}

	f.FileId.Type = fit.FileTypeActivity
	f.FileId.Manufacturer = fit.ManufacturerDevelopment
	f.FileId.Product = 0
	f.FileId.TimeCreated = start
	f.FileId.SerialNumber = 1

	act, err := f.Activity()
	if err != nil {
		return nil, fmt.Errorf("fit activity: %w", err)
	}

	var firstLat, firstLng *float64
	for i := range s.Samples {
		p := &s.Samples[i]
		r := fit.NewRecordMsg()
		r.Timestamp = p.Timestamp
		if p.Latitude != nil && p.Longitude != nil {
			r.PositionLat = fit.NewLatitudeDegrees(*p.Latitude)
			r.PositionLong = fit.NewLongitudeDegrees(*p.Longitude)
			if firstLat == nil {
				firstLat, firstLng = p.Latitude, p.Longitude
			}
		}
		if p.AltitudeM != nil {
			r.Altitude = scaleAltitude(*p.AltitudeM)
		}
		if p.DistanceM != nil {
			r.Distance = uint32(*p.DistanceM * 100)
		}
		if p.SpeedMps != nil {
			r.Speed = clampU16(*p.SpeedMps * 1000)
		}
		if p.HeartRateBpm != nil && *p.HeartRateBpm > 0 {
			r.HeartRate = clampU8(float64(*p.HeartRateBpm))
		}
		if p.Cadence != nil && *p.Cadence >= 0 {
			r.Cadence = clampU8(float64(*p.Cadence))
		}
		if p.PowerW != nil && *p.PowerW >= 0 {
			r.Power = clampU16(float64(*p.PowerW))
		}
		if p.TemperatureC != nil {
			r.Temperature = clampI8(float64(*p.TemperatureC))
		}
		act.Records = append(act.Records, r)
	}

	elapsed := uint32(a.ElapsedDuration.Seconds() * 1000)
	timer := uint32(a.MovingDuration.Seconds() * 1000)
	var dist uint32
	if a.Summary.DistanceM != nil {
		dist = uint32(*a.Summary.DistanceM * 100)
	}
	sport := fitSport(a)

	lap := fit.NewLapMsg()
	lap.StartTime = start
	lap.Timestamp = end
	lap.TotalElapsedTime = elapsed
	lap.TotalTimerTime = timer
	lap.TotalDistance = dist
	act.Laps = append(act.Laps, lap)

	ses := fit.NewSessionMsg()
	ses.StartTime = start
	ses.Timestamp = end
	ses.Sport = sport
	ses.TotalElapsedTime = elapsed
	ses.TotalTimerTime = timer
	ses.TotalDistance = dist
	ses.NumLaps = 1
	if firstLat != nil {
		ses.StartPositionLat = fit.NewLatitudeDegrees(*firstLat)
		ses.StartPositionLong = fit.NewLongitudeDegrees(*firstLng)
	}
	act.Sessions = append(act.Sessions, ses)

	am := fit.NewActivityMsg()
	am.Timestamp = end
	am.TotalTimerTime = timer
	am.NumSessions = 1
	am.Type = fit.ActivityModeManual
	am.Event = fit.EventActivity
	am.EventType = fit.EventTypeStop
	act.Activity = am

	var buf bytes.Buffer
	if err := fit.Encode(&buf, f, binary.LittleEndian); err != nil {
		return nil, fmt.Errorf("fit encode: %w", err)
	}
	return buf.Bytes(), nil
}

// scaleAltitude converts metres to the FIT altitude raw uint16 (scale 5,
// offset 500): raw = (m + 500) * 5. Clamped to the uint16 range.
func scaleAltitude(m float64) uint16 {
	return clampU16((m + 500) * 5)
}

func clampU16(v float64) uint16 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint16-1 {
		return math.MaxUint16 - 1
	}
	return uint16(v)
}

func clampU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 254 {
		return 254
	}
	return uint8(v)
}

func clampI8(v float64) int8 {
	if v < -127 {
		return -127
	}
	if v > 127 {
		return 127
	}
	return int8(v)
}

func fitSport(a domain.Activity) fit.Sport {
	switch a.Type {
	case domain.ActivityTypeRun:
		return fit.SportRunning
	case domain.ActivityTypeRide:
		return fit.SportCycling
	default:
		return fit.SportGeneric
	}
}
