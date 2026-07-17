package main

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
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

	// maxGap caps the seconds a single sample can contribute, so recording
	// pauses (large timestamp gaps) don't inflate a zone.
	const maxGap = 10.0

	mux.HandleFunc("GET /api/activities/{id}/zones", func(w http.ResponseWriter, r *http.Request) {
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

		// Per-sample dt from consecutive timestamps (clamped). Collect HR and
		// power value series alongside their dt, plus paired (HR, output) series
		// for aerobic-decoupling (power-paired and speed-paired).
		var hrVals, hrDts, pwVals, pwDts []float64
		var dHRp, dOutp, dHRs, dOuts []float64
		samples := stream.Samples
		for i, s := range samples {
			dt := 1.0
			if i+1 < len(samples) {
				dt = samples[i+1].Timestamp.Sub(s.Timestamp).Seconds()
				if dt <= 0 || dt > maxGap {
					dt = 1.0
				}
			}
			hr := 0.0
			if s.HeartRateBpm != nil && *s.HeartRateBpm > 0 {
				hr = float64(*s.HeartRateBpm)
				hrVals = append(hrVals, hr)
				hrDts = append(hrDts, dt)
			}
			if s.PowerW != nil && *s.PowerW > 0 {
				pwVals = append(pwVals, float64(*s.PowerW))
				pwDts = append(pwDts, dt)
				if hr > 0 {
					dHRp = append(dHRp, hr)
					dOutp = append(dOutp, float64(*s.PowerW))
				}
			}
			// Pace decoupling: only count genuinely-moving samples (>1 m/s ≈
			// 3.6 km/h) so stops/standstills don't distort the efficiency factor.
			if hr > 0 && s.SpeedMps != nil && *s.SpeedMps > 1.0 {
				dHRs = append(dHRs, hr)
				dOuts = append(dOuts, *s.SpeedMps)
			}
		}

		// Resolve thresholds at the activity's date.
		var prof *domain.AthleteProfile
		if app.AthleteProfiles != nil {
			if entries, e := app.AthleteProfiles.ListEntries(r.Context(), userID); e == nil {
				prof = domain.NewAthleteProfile(entries)
			}
		}
		valAt := func(k domain.AthleteMetricKey) (float64, bool) {
			if prof == nil {
				return 0, false
			}
			return prof.ValueAt(k, act.StartTime)
		}

		resp := map[string]any{"hr": nil, "power": nil}

		// Heart-rate zones: prefer LTHR, fall back to max HR.
		if len(hrVals) > 0 {
			ref, basis := 0.0, ""
			if v, ok := valAt(domain.AthleteThresholdHR); ok {
				ref, basis = v, "lthr"
			} else if v, ok := valAt(domain.AthleteMaxHR); ok {
				ref, basis = v, "max"
			}
			if ref > 0 {
				bands := domain.HRZoneBands(basis)
				resp["hr"] = zonePayload(basis, ref, bands, domain.BucketSeconds(bands, ref, hrVals, hrDts))
			}
		}

		// Power zones: need FTP.
		if len(pwVals) > 0 {
			if ftp, ok := valAt(domain.AthleteFTPWatts); ok && ftp > 0 {
				bands := domain.PowerZoneBands()
				resp["power"] = zonePayload("ftp", ftp, bands, domain.BucketSeconds(bands, ftp, pwVals, pwDts))
			}
		}

		// Aerobic decoupling — power-paired preferred (rides), else speed-paired
		// (runs). Only surfaced for a sustained, steady effort: require ≥10 min
		// of paired data and a plausible magnitude (|pct|≤30). Outside that the
		// activity wasn't steady enough for the metric to mean anything (intervals,
		// stop-go, very short) and showing a number would mislead. <5% = coupled.
		setDecoupling := func(hr, out []float64, basis string) bool {
			if len(hr) < 600 {
				return false
			}
			d, ok := domain.AerobicDecoupling(hr, out)
			if !ok || d < -30 || d > 30 {
				return false
			}
			resp["decoupling"] = map[string]any{"pct": round1(d), "basis": basis, "coupled": d < 5}
			return true
		}
		_ = setDecoupling(dHRp, dOutp, "power") || setDecoupling(dHRs, dOuts, "pace")

		writeJSON(w, http.StatusOK, resp)
	})

	logger.Info("zones endpoint mounted")
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
