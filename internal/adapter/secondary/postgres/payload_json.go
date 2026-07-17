package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// ---------------------------------------------------------------------------
// activity_sources.parsed (jsonb)
//
// payloadJSON is the on-disk shape for domain.ActivitySourcePayload. The
// "v" field is a schema version that future migrations can bump if the
// shape needs to evolve without rewriting old rows.
// ---------------------------------------------------------------------------

const payloadSchemaVersion = 1

type payloadJSON struct {
	V int `json:"v"`

	Type          string `json:"type"`
	Discipline    string `json:"discipline,omitempty"`
	IsVirtual     bool   `json:"is_virtual,omitempty"`
	IsEbike       bool   `json:"is_ebike,omitempty"`
	IsCommute     bool   `json:"is_commute,omitempty"`
	IsRace        bool   `json:"is_race,omitempty"`
	CustomSubtype string `json:"custom_subtype,omitempty"`

	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	ElapsedDurationS int64     `json:"elapsed_duration_s"`
	MovingDurationS  int64     `json:"moving_duration_s"`
	Timezone         string    `json:"timezone"`

	Summary   summaryJSON `json:"summary"`
	HasStream bool        `json:"has_stream,omitempty"`
	Laps      []lapJSON   `json:"laps,omitempty"`
}

type summaryJSON struct {
	DistanceM      *float64 `json:"distance_m,omitempty"`
	ElevationGainM *float64 `json:"elevation_gain_m,omitempty"`
	ElevationLossM *float64 `json:"elevation_loss_m,omitempty"`
	MinElevationM  *float64 `json:"min_elevation_m,omitempty"`
	MaxElevationM  *float64 `json:"max_elevation_m,omitempty"`

	AvgSpeedMps *float64 `json:"avg_speed_mps,omitempty"`
	MaxSpeedMps *float64 `json:"max_speed_mps,omitempty"`

	AvgHeartRateBpm *int `json:"avg_heart_rate_bpm,omitempty"`
	MaxHeartRateBpm *int `json:"max_heart_rate_bpm,omitempty"`

	AvgPowerW        *int `json:"avg_power_w,omitempty"`
	MaxPowerW        *int `json:"max_power_w,omitempty"`
	NormalizedPowerW *int `json:"normalized_power_w,omitempty"`

	AvgCadence *int `json:"avg_cadence,omitempty"`
	MaxCadence *int `json:"max_cadence,omitempty"`

	AvgTemperatureC *float64 `json:"avg_temperature_c,omitempty"`
	MinTemperatureC *float64 `json:"min_temperature_c,omitempty"`
	MaxTemperatureC *float64 `json:"max_temperature_c,omitempty"`

	CaloriesKcal    *int     `json:"calories_kcal,omitempty"`
	TSS             *float64 `json:"tss,omitempty"`
	IntensityFactor *float64 `json:"intensity_factor,omitempty"`

	PoolLengthM  *int `json:"pool_length_m,omitempty"`
	TotalStrokes *int `json:"total_strokes,omitempty"`
}

type lapJSON struct {
	Index            int      `json:"index,omitempty"`
	Label            string   `json:"label,omitempty"`
	StartOffsetS     int64    `json:"start_offset_s"`
	ElapsedDurationS int64    `json:"elapsed_duration_s"`
	MovingDurationS  int64    `json:"moving_duration_s"`
	DistanceM        *float64 `json:"distance_m,omitempty"`
	AvgSpeedMps      *float64 `json:"avg_speed_mps,omitempty"`
	AvgHeartRateBpm  *int     `json:"avg_heart_rate_bpm,omitempty"`
	AvgPowerW        *int     `json:"avg_power_w,omitempty"`
	AvgCadence       *int     `json:"avg_cadence,omitempty"`
	ElevationGainM   *float64 `json:"elevation_gain_m,omitempty"`
	ElevationLossM   *float64 `json:"elevation_loss_m,omitempty"`
}

// encodePayload marshals a domain payload into the on-disk JSON shape.
func encodePayload(p domain.ActivitySourcePayload) ([]byte, error) {
	w := payloadJSON{
		V:                payloadSchemaVersion,
		Type:             string(p.Type),
		Discipline:       string(p.Discipline),
		IsVirtual:        p.IsVirtual,
		IsEbike:          p.IsEbike,
		IsCommute:        p.IsCommute,
		IsRace:           p.IsRace,
		CustomSubtype:    p.CustomSubtype,
		Title:            p.Title,
		Description:      p.Description,
		StartTime:        p.StartTime,
		EndTime:          p.EndTime,
		ElapsedDurationS: int64(p.ElapsedDuration / time.Second),
		MovingDurationS:  int64(p.MovingDuration / time.Second),
		Timezone:         p.Timezone,
		Summary: summaryJSON{
			DistanceM:        p.Summary.DistanceM,
			ElevationGainM:   p.Summary.ElevationGainM,
			ElevationLossM:   p.Summary.ElevationLossM,
			MinElevationM:    p.Summary.MinElevationM,
			MaxElevationM:    p.Summary.MaxElevationM,
			AvgSpeedMps:      p.Summary.AvgSpeedMps,
			MaxSpeedMps:      p.Summary.MaxSpeedMps,
			AvgHeartRateBpm:  p.Summary.AvgHeartRateBpm,
			MaxHeartRateBpm:  p.Summary.MaxHeartRateBpm,
			AvgPowerW:        p.Summary.AvgPowerW,
			MaxPowerW:        p.Summary.MaxPowerW,
			NormalizedPowerW: p.Summary.NormalizedPowerW,
			AvgCadence:       p.Summary.AvgCadence,
			MaxCadence:       p.Summary.MaxCadence,
			AvgTemperatureC:  p.Summary.AvgTemperatureC,
			MinTemperatureC:  p.Summary.MinTemperatureC,
			MaxTemperatureC:  p.Summary.MaxTemperatureC,
			CaloriesKcal:     p.Summary.CaloriesKcal,
			TSS:              p.Summary.TSS,
			IntensityFactor:  p.Summary.IntensityFactor,
			PoolLengthM:      p.Summary.PoolLengthM,
			TotalStrokes:     p.Summary.TotalStrokes,
		},
		HasStream: p.HasStream,
	}
	if len(p.Laps) > 0 {
		w.Laps = make([]lapJSON, len(p.Laps))
		for i, l := range p.Laps {
			w.Laps[i] = lapJSON{
				Index:            l.Index,
				Label:            l.Label,
				StartOffsetS:     int64(l.StartOffset / time.Second),
				ElapsedDurationS: int64(l.ElapsedDuration / time.Second),
				MovingDurationS:  int64(l.MovingDuration / time.Second),
				DistanceM:        l.DistanceM,
				AvgSpeedMps:      l.AvgSpeedMps,
				AvgHeartRateBpm:  l.AvgHeartRateBpm,
				AvgPowerW:        l.AvgPowerW,
				AvgCadence:       l.AvgCadence,
				ElevationGainM:   l.ElevationGainM,
				ElevationLossM:   l.ElevationLossM,
			}
		}
	}
	return json.Marshal(w)
}

// decodePayload unmarshals an on-disk row into a domain payload.
func decodePayload(b []byte) (domain.ActivitySourcePayload, error) {
	if len(b) == 0 {
		return domain.ActivitySourcePayload{}, fmt.Errorf("empty payload jsonb")
	}
	var w payloadJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return domain.ActivitySourcePayload{}, fmt.Errorf("unmarshal payload: %w", err)
	}
	if w.V != 0 && w.V != payloadSchemaVersion {
		return domain.ActivitySourcePayload{}, fmt.Errorf("payload schema version %d not supported (this binary expects %d)", w.V, payloadSchemaVersion)
	}

	p := domain.ActivitySourcePayload{
		Type:            domain.ActivityType(w.Type),
		Discipline:      domain.Discipline(w.Discipline),
		IsVirtual:       w.IsVirtual,
		IsEbike:         w.IsEbike,
		IsCommute:       w.IsCommute,
		IsRace:          w.IsRace,
		CustomSubtype:   w.CustomSubtype,
		Title:           w.Title,
		Description:     w.Description,
		StartTime:       w.StartTime,
		EndTime:         w.EndTime,
		ElapsedDuration: time.Duration(w.ElapsedDurationS) * time.Second,
		MovingDuration:  time.Duration(w.MovingDurationS) * time.Second,
		Timezone:        w.Timezone,
		Summary: domain.ActivitySummary{
			DistanceM:        w.Summary.DistanceM,
			ElevationGainM:   w.Summary.ElevationGainM,
			ElevationLossM:   w.Summary.ElevationLossM,
			MinElevationM:    w.Summary.MinElevationM,
			MaxElevationM:    w.Summary.MaxElevationM,
			AvgSpeedMps:      w.Summary.AvgSpeedMps,
			MaxSpeedMps:      w.Summary.MaxSpeedMps,
			AvgHeartRateBpm:  w.Summary.AvgHeartRateBpm,
			MaxHeartRateBpm:  w.Summary.MaxHeartRateBpm,
			AvgPowerW:        w.Summary.AvgPowerW,
			MaxPowerW:        w.Summary.MaxPowerW,
			NormalizedPowerW: w.Summary.NormalizedPowerW,
			AvgCadence:       w.Summary.AvgCadence,
			MaxCadence:       w.Summary.MaxCadence,
			AvgTemperatureC:  w.Summary.AvgTemperatureC,
			MinTemperatureC:  w.Summary.MinTemperatureC,
			MaxTemperatureC:  w.Summary.MaxTemperatureC,
			CaloriesKcal:     w.Summary.CaloriesKcal,
			TSS:              w.Summary.TSS,
			IntensityFactor:  w.Summary.IntensityFactor,
			PoolLengthM:      w.Summary.PoolLengthM,
			TotalStrokes:     w.Summary.TotalStrokes,
		},
		HasStream: w.HasStream,
	}
	if len(w.Laps) > 0 {
		p.Laps = make([]domain.ActivityLap, len(w.Laps))
		for i, l := range w.Laps {
			p.Laps[i] = domain.ActivityLap{
				Index:           l.Index,
				Label:           l.Label,
				StartOffset:     time.Duration(l.StartOffsetS) * time.Second,
				ElapsedDuration: time.Duration(l.ElapsedDurationS) * time.Second,
				MovingDuration:  time.Duration(l.MovingDurationS) * time.Second,
				DistanceM:       l.DistanceM,
				AvgSpeedMps:     l.AvgSpeedMps,
				AvgHeartRateBpm: l.AvgHeartRateBpm,
				AvgPowerW:       l.AvgPowerW,
				AvgCadence:      l.AvgCadence,
				ElevationGainM:  l.ElevationGainM,
				ElevationLossM:  l.ElevationLossM,
			}
		}
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// activities.merge_provenance (jsonb)
//
// Encoded as a flat string map: FieldGroup -> UUID string. Empty
// provenance encodes as "{}".
// ---------------------------------------------------------------------------

// encodeProvenance turns a domain MergeProvenance into JSON bytes.
func encodeProvenance(m domain.MergeProvenance) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v.UUID().String()
	}
	return json.Marshal(out)
}

// decodeProvenance turns JSON bytes from the DB into a domain MergeProvenance.
// Empty / null jsonb is tolerated and returns an empty map.
func decodeProvenance(b []byte) (domain.MergeProvenance, error) {
	if len(b) == 0 || string(b) == "null" {
		return domain.MergeProvenance{}, nil
	}
	var raw map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal merge_provenance: %w", err)
	}
	out := make(domain.MergeProvenance, len(raw))
	for k, v := range raw {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("merge_provenance[%s] not a uuid: %w", k, err)
		}
		out[domain.FieldGroup(k)] = domain.SourceID(id)
	}
	return out, nil
}
