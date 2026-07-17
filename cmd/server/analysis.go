package main

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountAnalysis powers the Analysis page: the daily training-load series
// (CTL = fitness, ATL = fatigue, TSB = form) plus daily TSS, for tracking real
// fitness progress over time.
//
//	GET /api/analysis?days=N   (default 180)
//	  → { dates:[...], ctl:[...], atl:[...], tsb:[...], tss:[...],
//	      current:{ctl,atl,tsb} }
func mountAnalysis(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("GET /api/analysis", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 || days > 3650 {
			days = 180
		}
		end := time.Now().UTC().Add(24 * time.Hour)
		start := end.AddDate(0, 0, -days)

		metrics, err := app.Metrics.ListMetricsForUser(r.Context(), userID, "", start, end)
		if err != nil {
			http.Error(w, "analysis failed", http.StatusInternalServerError)
			return
		}

		type pt struct{ ctl, atl, tsb, tss float64 }
		byDay := map[string]*pt{}
		var order []string
		for _, mtr := range metrics {
			d := mtr.Timestamp.UTC().Format("2006-01-02")
			p, ok := byDay[d]
			if !ok {
				p = &pt{}
				byDay[d] = p
				order = append(order, d)
			}
			v := 0.0
			if mtr.ValueNumeric != nil {
				v = *mtr.ValueNumeric
			}
			switch mtr.Key {
			case domain.MetricKeyTrainingLoadCTL:
				p.ctl = v
			case domain.MetricKeyTrainingLoadATL:
				p.atl = v
			case domain.MetricKeyTrainingLoadTSB:
				p.tsb = v
			case "training_load.tss":
				p.tss = v
			}
		}

		dates := make([]string, 0, len(order))
		ctl := make([]float64, 0, len(order))
		atl := make([]float64, 0, len(order))
		tsb := make([]float64, 0, len(order))
		tss := make([]float64, 0, len(order))
		for _, d := range order {
			p := byDay[d]
			dates = append(dates, d)
			ctl = append(ctl, round1(p.ctl))
			atl = append(atl, round1(p.atl))
			tsb = append(tsb, round1(p.tsb))
			tss = append(tss, round1(p.tss))
		}

		current := map[string]any{"ctl": 0.0, "atl": 0.0, "tsb": 0.0}
		if n := len(order); n > 0 {
			p := byDay[order[n-1]]
			current = map[string]any{"ctl": round1(p.ctl), "atl": round1(p.atl), "tsb": round1(p.tsb)}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"dates": dates, "ctl": ctl, "atl": atl, "tsb": tsb, "tss": tss,
			"current": current,
		})
	})

	logger.Info("analysis endpoint mounted")
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
