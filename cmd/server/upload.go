package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/tormoder/fit"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	workerv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/worker/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountUploadEndpoint registers the drag-and-drop file import:
//
//	POST /api/activities/upload   multipart "file" = a .gpx/.tcx
//
// The file is parsed server-side into the typed ImportedActivity and run
// through the exact same ingest path workers use (provider = "upload",
// external_id = the file's content hash so re-uploading dedups/refreshes).
//
// Session-authed: the cairn_session cookie resolves the owning user.
func mountUploadEndpoint(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("POST /api/activities/upload", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		// Enforce the per-user activity quota before doing any parsing work.
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

		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(w, "bad multipart form: "+err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer file.Close()

		raw, err := io.ReadAll(io.LimitReader(file, 64<<20))
		if err != nil {
			http.Error(w, "read file: "+err.Error(), http.StatusBadRequest)
			return
		}

		payload, stream, err := parseUpload(header.Filename, raw)
		if err != nil {
			http.Error(w, "parse failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		sum := sha256.Sum256(raw)
		extID := hex.EncodeToString(sum[:16])

		jr := &workerv1.JobResult{
			WorkerName:    "file-upload",
			WorkerVersion: "1",
		}
		ev := &workerv1.ImportedActivity{
			Ref: &workerv1.ExternalRef{
				UserId:     userID.String(),
				Provider:   "upload",
				ExternalId: extID,
			},
			Payload: payload,
			Stream:  stream,
		}

		out, err := ingestActivityEvent(r.Context(), app, logger, jr, ev)
		if err != nil {
			logger.Error("upload ingest failed", "user_id", userID, "file", header.Filename, "error", err)
			http.Error(w, "ingest failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Archive the original file (best-effort — the activity is already
		// ingested). Server-side Put because the server holds the bytes; the
		// raw_blob_id lets the user download the original later.
		if app.BlobStore != nil {
			ext := ""
			if i := strings.LastIndex(header.Filename, "."); i >= 0 {
				ext = strings.ToLower(header.Filename[i:])
			}
			ct := uploadContentType(ext)
			key := fmt.Sprintf("raw/%s/%s%s", userID.String(), out.SourceID.String(), ext)
			if err := app.BlobStore.Put(r.Context(), key, raw, ct); err != nil {
				logger.Warn("archive raw upload failed", "source_id", out.SourceID, "error", err)
			} else if err := app.Activities.SetSourceRawBlob(r.Context(), out.SourceID, key, ct, int64(len(raw))); err != nil {
				logger.Warn("record raw blob ref failed", "source_id", out.SourceID, "error", err)
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"activity_id": out.ActivityID.String(),
			"source_id":   out.SourceID.String(),
			"action":      string(out.Action),
			"filename":    header.Filename,
		})
	})

	logger.Info("file upload endpoint mounted", "path", "/api/activities/upload")
}

// uploadContentType maps a file extension to a content type for the archived
// raw blob (so a later download serves it with a sensible type).
func uploadContentType(ext string) string {
	switch ext {
	case ".gpx":
		return "application/gpx+xml"
	case ".tcx":
		return "application/vnd.garmin.tcx+xml"
	case ".fit":
		return "application/vnd.ant.fit"
	default:
		return "application/octet-stream"
	}
}

// resolveSessionUser reads the cairn_session cookie and returns the owning
// user id if the session is active.
func resolveSessionUser(r *http.Request, app *App) (domain.UserID, bool) {
	// 1. Session cookie (browser).
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		hash := sha256.Sum256([]byte(c.Value))
		if sess, err := app.Sessions.GetByTokenHash(r.Context(), hash[:]); err == nil && sess.IsActive(time.Now().UTC()) {
			return sess.UserID, true
		}
	}
	// 2. Personal access token (CLI/API clients).
	if uid, ok := resolvePAT(r, app); ok {
		return uid, true
	}
	// 3. OAuth 2.1 access token (native/third-party). Read-only tokens may only
	// use safe methods; mutating REST calls require a write scope.
	return resolveOAuthREST(r, app)
}

// resolveOAuthREST authenticates an OAuth access token for REST handlers. A
// token without any write scope is limited to GET/HEAD requests, so read-only
// grants stay read-only across the REST surface.
func resolveOAuthREST(r *http.Request, app *App) (domain.UserID, bool) {
	if app.OAuth == nil {
		return domain.UserID{}, false
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(tok, oauthAccessTokenPrefix) {
		return domain.UserID{}, false
	}
	hash := sha256.Sum256([]byte(tok))
	at, err := app.OAuth.FindAccessToken(r.Context(), hash[:])
	if err != nil || !at.IsValidAt(time.Now().UTC()) {
		return domain.UserID{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !domain.ScopesHaveAnyWrite(at.Scope) {
		return domain.UserID{}, false
	}
	return at.UserID, true
}

// patTokenPrefix marks a personal access token so the resolver can tell it
// apart from other bearer tokens.
const patTokenPrefix = "cairn_pat_"

// resolvePAT authenticates an `Authorization: Bearer cairn_pat_...` request.
func resolvePAT(r *http.Request, app *App) (domain.UserID, bool) {
	if app.PATs == nil {
		return domain.UserID{}, false
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(tok, patTokenPrefix) {
		return domain.UserID{}, false
	}
	hash := sha256.Sum256([]byte(tok))
	pat, err := app.PATs.FindByTokenHash(r.Context(), hash[:])
	if err != nil || !pat.IsValidAt(time.Now().UTC()) {
		return domain.UserID{}, false
	}
	_ = app.PATs.TouchLastUsed(r.Context(), pat.ID)
	return pat.UserID, true
}

// parseUpload dispatches on file extension / content sniffing.
func parseUpload(filename string, raw []byte) (*cairnv1.ActivitySourcePayload, *cairnv1.ActivityStream, error) {
	name := strings.ToLower(filename)
	head := strings.ToLower(string(raw[:min(512, len(raw))]))
	switch {
	case strings.HasSuffix(name, ".gpx") || strings.Contains(head, "<gpx"):
		return parseGPX(raw)
	case strings.HasSuffix(name, ".tcx") || strings.Contains(head, "trainingcenterdatabase"):
		return parseTCX(raw)
	case strings.HasSuffix(name, ".fit") || (len(raw) > 12 && string(raw[8:12]) == ".FIT"):
		return parseFIT(raw)
	}
	return nil, nil, fmt.Errorf("unrecognised file type %q (supported: .gpx, .tcx, .fit)", filename)
}

// ---------------------------------------------------------------------------
// GPX
// ---------------------------------------------------------------------------

type gpxFile struct {
	XMLName xml.Name `xml:"gpx"`
	Trk     []struct {
		Name string `xml:"name"`
		Type string `xml:"type"`
		Seg  []struct {
			Pt []gpxPt `xml:"trkpt"`
		} `xml:"trkseg"`
	} `xml:"trk"`
}

type gpxPt struct {
	Lat  float64  `xml:"lat,attr"`
	Lon  float64  `xml:"lon,attr"`
	Ele  *float64 `xml:"ele"`
	Time string   `xml:"time"`
	Ext  struct {
		// Garmin TrackPointExtension (matched by local name, ignoring ns).
		HR    *int     `xml:"TrackPointExtension>hr"`
		Cad   *int     `xml:"TrackPointExtension>cad"`
		ATemp *float64 `xml:"TrackPointExtension>atemp"`
		// Power conventions vary across exporters.
		Power        *int `xml:"power"`
		PowerInWatts *int `xml:"PowerInWatts"`
	} `xml:"extensions"`
}

func parseGPX(raw []byte) (*cairnv1.ActivitySourcePayload, *cairnv1.ActivityStream, error) {
	var g gpxFile
	if err := xml.Unmarshal(raw, &g); err != nil {
		return nil, nil, fmt.Errorf("gpx: %w", err)
	}
	if len(g.Trk) == 0 {
		return nil, nil, fmt.Errorf("gpx: no tracks")
	}

	var pts []gpxPt
	for _, trk := range g.Trk {
		for _, seg := range trk.Seg {
			pts = append(pts, seg.Pt...)
		}
	}
	if len(pts) == 0 {
		return nil, nil, fmt.Errorf("gpx: no track points")
	}

	b := newActivityBuilder(g.Trk[0].Name, g.Trk[0].Type)
	for _, p := range pts {
		t, _ := time.Parse(time.RFC3339, p.Time)
		var power *int
		if p.Ext.Power != nil {
			power = p.Ext.Power
		} else if p.Ext.PowerInWatts != nil {
			power = p.Ext.PowerInWatts
		}
		b.add(samplePoint{
			Time: t, HasLatLon: true, Lat: p.Lat, Lon: p.Lon,
			Alt: p.Ele, HR: p.Ext.HR, Cad: p.Ext.Cad, Power: power, Temp: p.Ext.ATemp,
		})
	}
	return b.finish()
}

// ---------------------------------------------------------------------------
// TCX
// ---------------------------------------------------------------------------

type tcxFile struct {
	XMLName    xml.Name `xml:"TrainingCenterDatabase"`
	Activities struct {
		Activity []struct {
			Sport string `xml:"Sport,attr"`
			Lap   []struct {
				Track struct {
					Pt []tcxPt `xml:"Trackpoint"`
				} `xml:"Track"`
			} `xml:"Lap"`
		} `xml:"Activity"`
	} `xml:"Activities"`
}

type tcxPt struct {
	Time     string `xml:"Time"`
	Position *struct {
		Lat float64 `xml:"LatitudeDegrees"`
		Lon float64 `xml:"LongitudeDegrees"`
	} `xml:"Position"`
	Alt   *float64 `xml:"AltitudeMeters"`
	Dist  *float64 `xml:"DistanceMeters"`
	Cad   *int     `xml:"Cadence"`
	HRBpm *struct {
		Value int `xml:"Value"`
	} `xml:"HeartRateBpm"`
	Ext struct {
		Watts *int     `xml:"TPX>Watts"`
		Speed *float64 `xml:"TPX>Speed"`
	} `xml:"Extensions"`
}

func parseTCX(raw []byte) (*cairnv1.ActivitySourcePayload, *cairnv1.ActivityStream, error) {
	var x tcxFile
	if err := xml.Unmarshal(raw, &x); err != nil {
		return nil, nil, fmt.Errorf("tcx: %w", err)
	}
	if len(x.Activities.Activity) == 0 {
		return nil, nil, fmt.Errorf("tcx: no activities")
	}
	act := x.Activities.Activity[0]

	b := newActivityBuilder("", act.Sport)
	for _, lap := range act.Lap {
		for _, p := range lap.Track.Pt {
			t, _ := time.Parse(time.RFC3339, p.Time)
			sp := samplePoint{Time: t, Alt: p.Alt, Dist: p.Dist, Cad: p.Cad, Power: p.Ext.Watts, Speed: p.Ext.Speed}
			if p.Position != nil {
				sp.HasLatLon, sp.Lat, sp.Lon = true, p.Position.Lat, p.Position.Lon
			}
			if p.HRBpm != nil {
				hr := p.HRBpm.Value
				sp.HR = &hr
			}
			b.add(sp)
		}
	}
	return b.finish()
}

// ---------------------------------------------------------------------------
// FIT (Garmin/Wahoo/etc. binary)
// ---------------------------------------------------------------------------

func parseFIT(raw []byte) (*cairnv1.ActivitySourcePayload, *cairnv1.ActivityStream, error) {
	f, err := fit.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("fit: %w", err)
	}
	act, err := f.Activity()
	if err != nil {
		return nil, nil, fmt.Errorf("fit: not an activity file: %w", err)
	}

	sport := ""
	if len(act.Sessions) > 0 {
		sport = act.Sessions[0].Sport.String()
	}

	b := newActivityBuilder("", sport)
	for _, rec := range act.Records {
		if rec.Timestamp.IsZero() {
			continue
		}
		sp := samplePoint{Time: rec.Timestamp}
		if !rec.PositionLat.Invalid() && !rec.PositionLong.Invalid() {
			sp.HasLatLon = true
			sp.Lat = rec.PositionLat.Degrees()
			sp.Lon = rec.PositionLong.Degrees()
		}
		if v := rec.GetAltitudeScaled(); !math.IsNaN(v) {
			sp.Alt = &v
		}
		if v := rec.GetDistanceScaled(); !math.IsNaN(v) {
			sp.Dist = &v
		}
		if v := rec.GetSpeedScaled(); !math.IsNaN(v) {
			sp.Speed = &v
		}
		if rec.HeartRate != 0xFF {
			hr := int(rec.HeartRate)
			sp.HR = &hr
		}
		if rec.Cadence != 0xFF {
			c := int(rec.Cadence)
			sp.Cad = &c
		}
		if rec.Power != 0xFFFF {
			p := int(rec.Power)
			sp.Power = &p
		}
		if rec.Temperature != 0x7F {
			t := float64(rec.Temperature)
			sp.Temp = &t
		}
		b.add(sp)
	}
	return b.finish()
}

// ---------------------------------------------------------------------------
// Shared builder: points → typed payload + column stream
// ---------------------------------------------------------------------------

type samplePoint struct {
	Time      time.Time
	HasLatLon bool
	Lat, Lon  float64
	Alt       *float64
	Dist      *float64
	Speed     *float64
	HR        *int
	Cad       *int
	Power     *int
	Temp      *float64
}

type activityBuilder struct {
	name, sport string
	pts         []samplePoint
}

func newActivityBuilder(name, sport string) *activityBuilder {
	return &activityBuilder{name: name, sport: sport}
}

func (b *activityBuilder) add(p samplePoint) {
	if p.Time.IsZero() {
		return
	}
	b.pts = append(b.pts, p)
}

func (b *activityBuilder) finish() (*cairnv1.ActivitySourcePayload, *cairnv1.ActivityStream, error) {
	if len(b.pts) == 0 {
		return nil, nil, fmt.Errorf("no timestamped track points")
	}
	start := b.pts[0].Time
	end := b.pts[len(b.pts)-1].Time

	typ, disc := mapUploadSport(b.sport)
	title := b.name
	if title == "" {
		title = "Imported Activity"
	}

	n := len(b.pts)
	st := &cairnv1.ActivityStream{SampleCount: int32(n), ResolutionHz: 1.0}
	st.TimeS = make([]float64, n)

	var (
		lat, lon, alt, dist, speed                    = mkF(n), mkF(n), mkF(n), mkF(n), mkF(n)
		hr, power, cad                                = mkI(n), mkI(n), mkI(n)
		temp                                          = mkF(n)
		haveLL, haveAlt, haveDist                     bool
		haveSpeed, haveHR, havePow, haveCad, haveTemp bool
		cumDist                                       float64
		sumHR, cntHR, sumPow, cntPow                  float64
		elevGain                                      float64
		prevAlt                                       *float64
	)

	for i, p := range b.pts {
		st.TimeS[i] = p.Time.Sub(start).Seconds()
		if p.HasLatLon {
			lat[i], lon[i] = p.Lat, p.Lon
			haveLL = true
			if i > 0 && b.pts[i-1].HasLatLon {
				cumDist += haversine(b.pts[i-1].Lat, b.pts[i-1].Lon, p.Lat, p.Lon)
			}
		}
		if p.Dist != nil {
			dist[i] = *p.Dist
			haveDist = true
		} else if haveLL {
			dist[i] = cumDist
			haveDist = true
		}
		if p.Alt != nil {
			alt[i] = *p.Alt
			haveAlt = true
			if prevAlt != nil && *p.Alt > *prevAlt {
				elevGain += *p.Alt - *prevAlt
			}
			prevAlt = p.Alt
		}
		if p.Speed != nil {
			speed[i] = *p.Speed
			haveSpeed = true
		}
		if p.HR != nil {
			hr[i] = int32(*p.HR)
			haveHR = true
			sumHR += float64(*p.HR)
			cntHR++
		}
		if p.Power != nil {
			power[i] = int32(*p.Power)
			havePow = true
			sumPow += float64(*p.Power)
			cntPow++
		}
		if p.Cad != nil {
			cad[i] = int32(*p.Cad)
			haveCad = true
		}
		if p.Temp != nil {
			temp[i] = *p.Temp
			haveTemp = true
		}
	}

	var ch []cairnv1.StreamChannel
	if haveLL {
		st.Latitude, st.Longitude = lat, lon
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_LATITUDE, cairnv1.StreamChannel_STREAM_CHANNEL_LONGITUDE)
	}
	if haveAlt {
		st.AltitudeM = alt
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_ALTITUDE)
	}
	if haveDist {
		st.DistanceM = dist
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_DISTANCE)
	}
	if haveSpeed {
		st.SpeedMps = speed
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_SPEED)
	}
	if haveHR {
		st.HeartRateBpm = hr
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_HEART_RATE)
	}
	if havePow {
		st.PowerW = power
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_POWER)
	}
	if haveCad {
		st.Cadence = cad
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_CADENCE)
	}
	if haveTemp {
		st.TemperatureC = temp
		ch = append(ch, cairnv1.StreamChannel_STREAM_CHANNEL_TEMPERATURE)
	}
	st.Channels = ch

	summary := &cairnv1.ActivitySummary{}
	if haveDist {
		d := dist[n-1]
		summary.DistanceM = &d
	}
	if haveAlt {
		summary.ElevationGainM = &elevGain
	}
	if cntHR > 0 {
		v := int32(sumHR / cntHR)
		summary.AvgHeartRateBpm = &v
	}
	if cntPow > 0 {
		v := int32(sumPow / cntPow)
		summary.AvgPowerW = &v
	}

	payload := &cairnv1.ActivitySourcePayload{
		Type:               typ,
		Discipline:         disc,
		ProviderNativeType: b.sport,
		Title:              title,
		StartTime:          timestamppb.New(start.UTC()),
		EndTime:            timestamppb.New(end.UTC()),
		ElapsedDuration:    durationpb.New(end.Sub(start)),
		MovingDuration:     durationpb.New(end.Sub(start)),
		Timezone:           "UTC",
		Summary:            summary,
	}
	return payload, st, nil
}

func mapUploadSport(sport string) (cairnv1.ActivityType, cairnv1.Discipline) {
	s := strings.ToLower(sport)
	switch {
	case strings.Contains(s, "cycl"), strings.Contains(s, "ride"), strings.Contains(s, "bike"), strings.Contains(s, "biking"):
		return cairnv1.ActivityType_ACTIVITY_TYPE_RIDE, cairnv1.Discipline_DISCIPLINE_RIDE_ROAD
	case strings.Contains(s, "run"):
		return cairnv1.ActivityType_ACTIVITY_TYPE_RUN, cairnv1.Discipline_DISCIPLINE_RUN_ROAD
	case strings.Contains(s, "hik"):
		return cairnv1.ActivityType_ACTIVITY_TYPE_HIKE, cairnv1.Discipline_DISCIPLINE_UNSPECIFIED
	case strings.Contains(s, "walk"):
		return cairnv1.ActivityType_ACTIVITY_TYPE_WALK, cairnv1.Discipline_DISCIPLINE_UNSPECIFIED
	case strings.Contains(s, "swim"):
		return cairnv1.ActivityType_ACTIVITY_TYPE_SWIM, cairnv1.Discipline_DISCIPLINE_UNSPECIFIED
	}
	return cairnv1.ActivityType_ACTIVITY_TYPE_WORKOUT, cairnv1.Discipline_DISCIPLINE_UNSPECIFIED
}

func mkF(n int) []float64 { return make([]float64, n) }
func mkI(n int) []int32   { return make([]int32, n) }

// note: min() is the Go builtin (1.21+).

// haversine returns the great-circle distance in metres.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
