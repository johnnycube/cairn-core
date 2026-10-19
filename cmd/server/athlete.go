package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/usecase/trainingload"
)

// mountAthlete exposes the athlete physiology profile: a per-user, time-series
// set of physiological values (FTP, weight, threshold HR, …) that feed the TSS
// estimate. Values are dated so calculations resolve them as of an activity's
// date.
//
//	GET    /api/athlete/metrics              → { specs:[...], entries:[...] }
//	PUT    /api/athlete/metrics              { key, effective_date, value }
//	DELETE /api/athlete/metrics/{id}
//	POST   /api/athlete/recalculate          re-derive TSS + training load
func mountAthlete(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.AthleteProfiles == nil {
		return
	}
	h := athleteHandlers{app: app, logger: logger}

	mux.HandleFunc("GET /api/athlete/metrics", h.listMetrics)
	mux.HandleFunc("PUT /api/athlete/metrics", h.putMetric)
	mux.HandleFunc("DELETE /api/athlete/metrics/{id}", h.deleteMetric)
	mux.HandleFunc("POST /api/athlete/recalculate", h.recalculate)

	logger.Info("athlete profile endpoints mounted")
}

// athleteHandlers holds the athlete profile HTTP handlers.
type athleteHandlers struct {
	app    *App
	logger *slog.Logger
}

// GET — the metric registry (for the UI form) plus the user's entries.
func (h athleteHandlers) listMetrics(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	entries, err := h.app.AthleteProfiles.ListEntries(r.Context(), userID)
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	specs := make([]map[string]any, 0, len(domain.AthleteMetricSpecs))
	for _, s := range domain.AthleteMetricSpecs {
		specs = append(specs, map[string]any{
			"key": string(s.Key), "label": s.Label, "unit": s.Unit, "min": s.Min, "max": s.Max,
		})
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id":             e.ID.String(),
			"key":            string(e.Key),
			"effective_date": e.EffectiveDate.UTC().Format("2006-01-02"),
			"value":          e.Value,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"specs": specs, "entries": out})
}

// PUT — upsert one dated measurement.
func (h athleteHandlers) putMetric(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var body struct {
		Key           string  `json:"key"`
		EffectiveDate string  `json:"effective_date"`
		Value         float64 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	day, err := time.Parse("2006-01-02", body.EffectiveDate)
	if err != nil {
		http.Error(w, "effective_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	entry := domain.AthleteMetricEntry{
		UserID:        userID,
		Key:           domain.AthleteMetricKey(body.Key),
		EffectiveDate: day.UTC(),
		Value:         body.Value,
	}
	if err := entry.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := h.app.AthleteProfiles.UpsertEntry(r.Context(), entry)
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id.String()})
}

// DELETE — remove one entry (ownership-scoped in the query).
func (h athleteHandlers) deleteMetric(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.app.AthleteProfiles.DeleteEntry(r.Context(), userID, domain.AthleteMetricID(id)); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/athlete/recalculate — re-derive TSS for every one of the user's
// activities using the now-current profile, then recompute the training-load
// curves. This is what makes editing the profile retroactively correct the
// CTL/ATL/TSB history.
func (h athleteHandlers) recalculate(w http.ResponseWriter, r *http.Request) {
	userID, ok := resolveSessionUser(r, h.app)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if h.app.RecomputeActivity == nil {
		http.Error(w, "recompute unavailable", http.StatusServiceUnavailable)
		return
	}
	// All of the user's activities (wide window covers full history).
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC().Add(24 * time.Hour)
	acts, err := h.app.Activities.ListActivitiesForUser(r.Context(), userID, start, end)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	earliest := end
	recomputed := 0
	for _, a := range acts {
		if _, err := h.app.RecomputeActivity.Execute(r.Context(), a.ID); err != nil {
			h.logger.Warn("athlete recalc: recompute failed", "activity", a.ID, "error", err)
			continue
		}
		if a.StartTime.Before(earliest) {
			earliest = a.StartTime
		}
		recomputed++
	}
	days := 0
	if h.app.ComputeTrainingLoad != nil && recomputed > 0 {
		res, err := h.app.ComputeTrainingLoad.Execute(r.Context(), trainingload.Input{
			UserID:     userID,
			Start:      earliest,
			End:        time.Now().UTC(),
			WarmUpDays: 42,
		})
		if err != nil {
			h.logger.Warn("athlete recalc: training load failed", "error", err)
		} else {
			days = res.DaysComputed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recomputed": recomputed, "training_load_days": days})
}
