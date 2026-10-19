package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
)

// mountZones exposes per-activity time-in-zone (heart-rate + power), computed
// from the activity's stream and the athlete's thresholds resolved AS OF the
// activity's date.
//
//	GET /api/activities/{id}/zones
//	  → { hr: {basis, reference, total_seconds, zones:[{label,low_pct,high_pct,seconds}]} | null,
//	      power: {...} | null }
func mountZones(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.Streams == nil {
		return
	}

	mux.HandleFunc("GET /api/activities/{id}/zones", zonesHandler(app))

	logger.Info("zones endpoint mounted")
}

// zonesMaxGap caps the seconds a single sample can contribute, so recording
// pauses (large timestamp gaps) don't inflate a zone.
const zonesMaxGap = 10.0

// zonesSeries holds the per-sample value/dt series derived from a stream.
type zonesSeries struct {
	hrVals, hrDts, pwVals, pwDts []float64
	dHRp, dOutp, dHRs, dOuts     []float64
}

// zonesSampleDt is the clamped seconds between sample i and the next one.
func zonesSampleDt(samples []domain.StreamSample, i int) float64 {
	if i+1 >= len(samples) {
		return 1.0
	}
	dt := samples[i+1].Timestamp.Sub(samples[i].Timestamp).Seconds()
	if dt <= 0 || dt > zonesMaxGap {
		return 1.0
	}
	return dt
}

// zonesCollect builds the series from the raw samples.
func zonesCollect(samples []domain.StreamSample) zonesSeries {
	// Per-sample dt from consecutive timestamps (clamped). Collect HR and
	// power value series alongside their dt, plus paired (HR, output) series
	// for aerobic-decoupling (power-paired and speed-paired).
	var z zonesSeries
	for i, s := range samples {
		dt := zonesSampleDt(samples, i)
		hr := 0.0
		if s.HeartRateBpm != nil && *s.HeartRateBpm > 0 {
			hr = float64(*s.HeartRateBpm)
			z.hrVals = append(z.hrVals, hr)
			z.hrDts = append(z.hrDts, dt)
		}
		if s.PowerW != nil && *s.PowerW > 0 {
			z.pwVals = append(z.pwVals, float64(*s.PowerW))
			z.pwDts = append(z.pwDts, dt)
			if hr > 0 {
				z.dHRp = append(z.dHRp, hr)
				z.dOutp = append(z.dOutp, float64(*s.PowerW))
			}
		}
		// Pace decoupling: only count genuinely-moving samples (>1 m/s ≈
		// 3.6 km/h) so stops/standstills don't distort the efficiency factor.
		if hr > 0 && s.SpeedMps != nil && *s.SpeedMps > 1.0 {
			z.dHRs = append(z.dHRs, hr)
			z.dOuts = append(z.dOuts, *s.SpeedMps)
		}
	}
	return z
}

// zonesThresholds resolves the athlete's thresholds at the activity's date.
func zonesThresholds(r *http.Request, app *App, userID domain.UserID, act domain.Activity) func(domain.AthleteMetricKey) (float64, bool) {
	var prof *domain.AthleteProfile
	if app.AthleteProfiles != nil {
		if entries, e := app.AthleteProfiles.ListEntries(r.Context(), userID); e == nil {
			prof = domain.NewAthleteProfile(entries)
		}
	}
	return func(k domain.AthleteMetricKey) (float64, bool) {
		if prof == nil {
			return 0, false
		}
		return prof.ValueAt(k, act.StartTime)
	}
}

// zonesHRPayload computes heart-rate zones: prefer LTHR, fall back to max HR.
func zonesHRPayload(z zonesSeries, valAt func(domain.AthleteMetricKey) (float64, bool)) (map[string]any, bool) {
	if len(z.hrVals) == 0 {
		return nil, false
	}
	ref, basis := 0.0, ""
	if v, ok := valAt(domain.AthleteThresholdHR); ok {
		ref, basis = v, "lthr"
	} else if v, ok := valAt(domain.AthleteMaxHR); ok {
		ref, basis = v, "max"
	}
	if ref <= 0 {
		return nil, false
	}
	bands := domain.HRZoneBands(basis)
	return zonePayload(basis, ref, bands, domain.BucketSeconds(bands, ref, z.hrVals, z.hrDts)), true
}

// zonesDecoupling returns the decoupling payload for a sustained, steady effort.
func zonesDecoupling(hr, out []float64, basis string) (map[string]any, bool) {
	if len(hr) < 600 {
		return nil, false
	}
	d, ok := domain.AerobicDecoupling(hr, out)
	if !ok || d < -30 || d > 30 {
		return nil, false
	}
	return map[string]any{"pct": round1(d), "basis": basis, "coupled": d < 5}, true
}

// zonesResponse assembles the zones response body.
func zonesResponse(z zonesSeries, valAt func(domain.AthleteMetricKey) (float64, bool)) map[string]any {
	resp := map[string]any{"hr": nil, "power": nil}

	if hr, ok := zonesHRPayload(z, valAt); ok {
		resp["hr"] = hr
	}

	// Power zones: need FTP.
	if len(z.pwVals) > 0 {
		if ftp, ok := valAt(domain.AthleteFTPWatts); ok && ftp > 0 {
			bands := domain.PowerZoneBands()
			resp["power"] = zonePayload("ftp", ftp, bands, domain.BucketSeconds(bands, ftp, z.pwVals, z.pwDts))
		}
	}

	// Aerobic decoupling — power-paired preferred (rides), else speed-paired
	// (runs). Only surfaced for a sustained, steady effort: require ≥10 min
	// of paired data and a plausible magnitude (|pct|≤30). Outside that the
	// activity wasn't steady enough for the metric to mean anything (intervals,
	// stop-go, very short) and showing a number would mislead. <5% = coupled.
	if d, ok := zonesDecoupling(z.dHRp, z.dOutp, "power"); ok {
		resp["decoupling"] = d
	} else if d, ok := zonesDecoupling(z.dHRs, z.dOuts, "pace"); ok {
		resp["decoupling"] = d
	}
	return resp
}

// zonesHandler serves GET /api/activities/{id}/zones.
func zonesHandler(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		act, err := app.Activities.GetActivity(r.Context(), domain.ActivityID(id))
		if err != nil || act.UserID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if act.PrimaryStreamSourceID == nil {
			writeJSON(w, http.StatusOK, map[string]any{"hr": nil, "power": nil})
			return
		}

		stream, err := app.Streams.QueryStream(r.Context(), domain.StreamQuery{
			ActivitySourceID: *act.PrimaryStreamSourceID,
			Channels:         []domain.StreamChannel{domain.StreamChannelHeartRate, domain.StreamChannelPower},
			Resolution:       domain.StreamResolutionRaw,
		})
		if err != nil {
			http.Error(w, "stream query failed", http.StatusInternalServerError)
			return
		}

		z := zonesCollect(stream.Samples)
		valAt := zonesThresholds(r, app, userID, act)
		writeJSON(w, http.StatusOK, zonesResponse(z, valAt))
	}
}

func zonePayload(basis string, ref float64, bands []domain.ZoneBand, secs []float64) map[string]any {
	total := 0.0
	zones := make([]map[string]any, len(bands))
	for i, b := range bands {
		total += secs[i]
		zones[i] = map[string]any{
			"label": b.Label, "low_pct": b.LowPct, "high_pct": b.HighPct,
			"seconds": int(secs[i] + 0.5),
		}
	}
	return map[string]any{
		"basis": basis, "reference": ref,
		"total_seconds": int(total + 0.5),
		"zones":         zones,
	}
}
