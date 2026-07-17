package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	workerv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/worker/v1"
)

// mountManualActivityEndpoint registers manual activity creation:
//
//	POST /api/activities/manual   JSON body (see manualActivityInput)
//
// A manual entry is modeled as a source with provider = "manual" feeding the
// exact same ingest → merge pipeline as worker/upload imports — so it dedups,
// re-merges, and is editable like any other activity. There is no stream; the
// user supplies summary values directly. external_id is a fresh UUID, so each
// submission creates a new activity (handleCreateNew).
func mountManualActivityEndpoint(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("POST /api/activities/manual", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		if app.Quotas != nil {
			if st := activityQuotaStatus(r.Context(), app, userID); st.OverLimit {
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error":            "activity quota reached",
					"activities_used":  st.Used,
					"activities_limit": st.Limit,
				})
				return
			}
		}

		var in manualActivityInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}

		payload, err := in.toPayload()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		jr := &workerv1.JobResult{WorkerName: "manual-entry", WorkerVersion: "1"}
		ev := &workerv1.ImportedActivity{
			Ref: &workerv1.ExternalRef{
				UserId:     userID.String(),
				Provider:   "manual",
				ExternalId: uuid.NewString(),
			},
			Payload: payload,
		}

		out, err := ingestActivityEvent(r.Context(), app, logger, jr, ev)
		if err != nil {
			logger.Error("manual ingest failed", "user_id", userID, "error", err)
			http.Error(w, "ingest failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"activity_id": out.ActivityID.String(),
			"source_id":   out.SourceID.String(),
		})
	})

	logger.Info("manual activity endpoint mounted", "path", "/api/activities/manual")
}

// manualActivityInput is the JSON body for a manually-entered activity. Only
// type, start_time and elapsed_duration_s are required; the rest are optional
// summary values.
type manualActivityInput struct {
	Type            string  `json:"type"`
	Discipline      string  `json:"discipline"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	StartTime       string  `json:"start_time"` // RFC3339
	Timezone        string  `json:"timezone"`
	ElapsedDuration float64 `json:"elapsed_duration_s"`
	MovingDuration  float64 `json:"moving_duration_s"`
	IsVirtual       bool    `json:"is_virtual"`
	IsEbike         bool    `json:"is_ebike"`
	IsCommute       bool    `json:"is_commute"`
	IsRace          bool    `json:"is_race"`
	CustomSubtype   string  `json:"custom_subtype"`

	DistanceM       *float64 `json:"distance_m"`
	ElevationGainM  *float64 `json:"elevation_gain_m"`
	AvgHeartRateBpm *int32   `json:"avg_heart_rate_bpm"`
	AvgPowerW       *int32   `json:"avg_power_w"`
	CaloriesKcal    *int32   `json:"calories_kcal"`
}

func (in manualActivityInput) toPayload() (*cairnv1.ActivitySourcePayload, error) {
	typ, ok := protoActivityType(in.Type)
	if !ok {
		return nil, fmt.Errorf("invalid or missing type %q", in.Type)
	}
	start, err := time.Parse(time.RFC3339, in.StartTime)
	if err != nil {
		return nil, fmt.Errorf("invalid start_time (want RFC3339): %v", err)
	}
	if in.ElapsedDuration <= 0 {
		return nil, fmt.Errorf("elapsed_duration_s must be > 0")
	}
	moving := in.MovingDuration
	if moving <= 0 || moving > in.ElapsedDuration {
		moving = in.ElapsedDuration
	}
	tz := in.Timezone
	if tz == "" {
		tz = "UTC"
	}

	disc := cairnv1.Discipline_DISCIPLINE_UNSPECIFIED
	if in.Discipline != "" {
		d, ok := protoDiscipline(in.Discipline)
		if !ok {
			return nil, fmt.Errorf("invalid discipline %q", in.Discipline)
		}
		disc = d
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Manual Activity"
	}

	elapsed := time.Duration(in.ElapsedDuration * float64(time.Second))
	summary := &cairnv1.ActivitySummary{
		DistanceM:       in.DistanceM,
		ElevationGainM:  in.ElevationGainM,
		AvgHeartRateBpm: in.AvgHeartRateBpm,
		AvgPowerW:       in.AvgPowerW,
		CaloriesKcal:    in.CaloriesKcal,
	}

	return &cairnv1.ActivitySourcePayload{
		Type:            typ,
		Discipline:      disc,
		IsVirtual:       in.IsVirtual,
		IsEbike:         in.IsEbike,
		IsCommute:       in.IsCommute,
		IsRace:          in.IsRace,
		CustomSubtype:   in.CustomSubtype,
		Title:           title,
		Description:     in.Description,
		StartTime:       timestamppb.New(start.UTC()),
		EndTime:         timestamppb.New(start.Add(elapsed).UTC()),
		ElapsedDuration: durationpb.New(elapsed),
		MovingDuration:  durationpb.New(time.Duration(moving * float64(time.Second))),
		Timezone:        tz,
		Summary:         summary,
	}, nil
}

// protoActivityType maps a lowercase domain type ("ride") to its proto enum,
// reporting ok=false for empty/unknown/UNSPECIFIED.
func protoActivityType(s string) (cairnv1.ActivityType, bool) {
	if s == "" {
		return 0, false
	}
	v, ok := cairnv1.ActivityType_value["ACTIVITY_TYPE_"+strings.ToUpper(s)]
	if !ok || v == 0 {
		return 0, false
	}
	return cairnv1.ActivityType(v), true
}

// protoDiscipline maps a lowercase discipline ("ride_road") to its proto enum.
func protoDiscipline(s string) (cairnv1.Discipline, bool) {
	v, ok := cairnv1.Discipline_value["DISCIPLINE_"+strings.ToUpper(s)]
	if !ok || v == 0 {
		return 0, false
	}
	return cairnv1.Discipline(v), true
}
