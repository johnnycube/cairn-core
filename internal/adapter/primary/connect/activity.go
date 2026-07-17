// Package connect contains the primary (driving) Connect-RPC adapters
// that wrap the application's use-cases and repositories for cairn.v1.*
// consumers — the SvelteKit frontend, future mobile clients, third-party
// integrations.
//
// The Connect adapter never owns business logic. Each method:
//
//  1. Authenticates the caller (today: X-Cairn-User-ID header; future:
//     session cookie / JWT).
//  2. Translates the proto request into domain inputs.
//  3. Calls into the existing use-case or repository.
//  4. Translates the domain result back into proto.
//
// The cairnv1connect.UnimplementedActivityServiceHandler base is embedded
// so unimplemented RPCs return CodeUnimplemented automatically — the
// surface grows method-by-method without touching every other RPC.
package connect

import (
	"context"
	"errors"
	"fmt"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/gen/proto/cairn/v1/cairnv1connect"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/activity"
)

// ActivityServer implements cairnv1connect.ActivityServiceHandler. The
// embedded Unimplemented base returns CodeUnimplemented for every RPC
// this struct doesn't override.
type ActivityServer struct {
	cairnv1connect.UnimplementedActivityServiceHandler

	Activities  port.ActivityRepo
	Streams     port.StreamRepo
	BestEfforts port.BestEffortRepo

	// Recompute re-runs the merge engine over an activity's sources after a
	// user edit, so the persisted row stays canonical and preserveUserEdits
	// keeps the edit across future source imports.
	Recompute *activity.RecomputeActivityFromSources

	// ClassOverrides persists the user's classification overlay (type/flags),
	// applied after the merge by Recompute so the edit survives re-derivation.
	ClassOverrides port.ClassificationOverrideRepo

	// Federation announces visibility changes to remote followers (nil when
	// federation is off). Lets making an activity public after import federate
	// it, since the ingest-time publish only fires for public-at-import.
	Federation port.FederationPublisher
}

// Compile-time interface check.
var _ cairnv1connect.ActivityServiceHandler = (*ActivityServer)(nil)

// NewActivityServer wires the server with its dependencies. Callers pass
// the concrete repos coming out of wire.go.
func NewActivityServer(
	activities port.ActivityRepo,
	streams port.StreamRepo,
	bestEfforts port.BestEffortRepo,
	recompute *activity.RecomputeActivityFromSources,
	classOverrides port.ClassificationOverrideRepo,
	federation port.FederationPublisher,
) *ActivityServer {
	return &ActivityServer{
		Activities:     activities,
		Streams:        streams,
		BestEfforts:    bestEfforts,
		Recompute:      recompute,
		ClassOverrides: classOverrides,
		Federation:     federation,
	}
}

// ---------------------------------------------------------------------------
// ListActivities
//
// Returns the user's activities whose start_time falls in the requested
// window. Pagination is not yet implemented — the underlying repo method
// returns the full match set, which is acceptable for v1 user-volumes.
// When window is empty/zero we default to "last 90 days".
// ---------------------------------------------------------------------------

const defaultListWindow = 90 * 24 * time.Hour

func (s *ActivityServer) ListActivities(
	ctx context.Context,
	req *connectrpc.Request[v1.ListActivitiesRequest],
) (*connectrpc.Response[v1.ListActivitiesResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	// No explicit time range → count-based feed (newest N regardless of age),
	// so older imports don't fall out of an arbitrary default window. An
	// explicit range still uses the time-windowed query.
	tr := req.Msg.GetTimeRange()
	var activities []domain.Activity
	if tr == nil || (tr.GetFrom() == nil && tr.GetTo() == nil) {
		limit := int(req.Msg.GetPage().GetLimit())
		activities, err = s.Activities.ListRecentActivitiesForUser(ctx, userID, limit)
	} else {
		start, end := windowFromRequest(tr)
		activities, err = s.Activities.ListActivitiesForUser(ctx, userID, start, end)
	}
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list activities: %w", err))
	}

	out := &v1.ListActivitiesResponse{
		Activities: make([]*v1.Activity, 0, len(activities)),
	}
	for _, a := range activities {
		var sources []domain.ActivitySource
		if req.Msg.GetIncludeSources() {
			srcs, err := s.Activities.ListSourcesForActivity(ctx, a.ID)
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal,
					fmt.Errorf("list sources for %s: %w", a.ID, err))
			}
			sources = srcs
		}
		out.Activities = append(out.Activities, activityToProto(a, sources))
	}

	// Reverse to start-time-DESC unless the caller asked otherwise. The
	// repo currently returns ASC; the proto default is DESC and the
	// frontend feed expects newest first.
	if req.Msg.GetSort() != v1.ActivitySort_ACTIVITY_SORT_START_TIME_ASC {
		reverseActivities(out.Activities)
	}

	return connectrpc.NewResponse(out), nil
}

// ---------------------------------------------------------------------------
// GetActivity
// ---------------------------------------------------------------------------

func (s *ActivityServer) GetActivity(
	ctx context.Context,
	req *connectrpc.Request[v1.GetActivityRequest],
) (*connectrpc.Response[v1.GetActivityResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("invalid activity id"))
	}

	activity, err := s.Activities.GetActivity(ctx, domain.ActivityID(id))
	if err != nil {
		return nil, notFoundErr(err)
	}

	// Authorisation: the activity must belong to the authenticated user.
	// Future RBAC (admin override, follower sharing) plugs in here.
	if activity.UserID != userID {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound,
			errors.New("not found"))
	}
	if activity.IsDeleted() {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound,
			errors.New("not found"))
	}

	sources, err := s.Activities.ListSourcesForActivity(ctx, activity.ID)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list sources: %w", err))
	}

	return connectrpc.NewResponse(&v1.GetActivityResponse{
		Activity: activityToProto(activity, sources),
	}), nil
}

// ---------------------------------------------------------------------------
// UpdateActivity
//
// Applies user edits to an activity and re-runs the merge so the edit
// survives future source imports (the merge engine's preserveUserEdits
// owns Title/Description/Tags/Privacy once an activity exists).
//
// Only field_mask entries are touched. v1 accepts the merge-preserved
// user-editable fields; the source-driven classification fields (type,
// discipline, the is_* flags, custom_subtype, primary_stream_source_id)
// are exposed on the proto for forward-compatibility but rejected here —
// the merge engine would overwrite them on the next import. Making those
// user-overridable needs per-field user-edit tracking in the merge engine
// (a future change), so we fail loudly rather than silently revert.
// ---------------------------------------------------------------------------

func (s *ActivityServer) UpdateActivity(
	ctx context.Context,
	req *connectrpc.Request[v1.UpdateActivityRequest],
) (*connectrpc.Response[v1.UpdateActivityResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("invalid activity id"))
	}
	activityID := domain.ActivityID(id)

	activity, err := s.Activities.GetActivity(ctx, activityID)
	if err != nil {
		return nil, notFoundErr(err)
	}
	if activity.UserID != userID || activity.IsDeleted() {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound, errors.New("not found"))
	}

	mask := req.Msg.GetFieldMask()
	if len(mask) == 0 {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("field_mask must list at least one field to update"))
	}

	// Capture the pre-edit privacy so a public⇄non-public transition can be
	// federated after the save.
	prevPrivacy := activity.Privacy
	privacyTouched := false

	// Classification edits (type/discipline/flags) are stored as a user overlay
	// applied AFTER the merge by Recompute, so they survive future source
	// imports. We start from the existing overlay and mutate only masked fields.
	var classOverride *domain.ClassificationOverride
	classTouched := false
	rejectClass := func(f string) error {
		return connectrpc.NewError(connectrpc.CodeInvalidArgument,
			fmt.Errorf("field %q is not editable on this instance", f))
	}
	ensureCO := func() (*domain.ClassificationOverride, error) {
		if classOverride == nil {
			existing, err := s.ClassOverrides.Get(ctx, activityID)
			if err != nil {
				return nil, err
			}
			classOverride = &existing
		}
		classTouched = true
		return classOverride, nil
	}

	for _, f := range mask {
		switch f {
		case "title":
			activity.Title = req.Msg.GetTitle()
		case "description":
			activity.Description = req.Msg.GetDescription()
		case "tags":
			// Present-in-mask replaces the whole set (empty clears it).
			activity.Tags = req.Msg.GetTags()
		case "privacy":
			p, ok := privacyFromProto(req.Msg.GetPrivacy())
			if !ok {
				return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
					errors.New("privacy must be one of private|followers|public"))
			}
			activity.Privacy = p
			privacyTouched = true
		case "type":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			if t, ok := activityTypeFromProto(req.Msg.GetType()); ok {
				co.Type = &t
			} else {
				co.Type = nil // UNSPECIFIED clears the override → revert to merged
			}
		case "discipline":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			if d, ok := disciplineFromProto(req.Msg.GetDiscipline()); ok {
				co.Discipline = &d
			} else {
				co.Discipline = nil
			}
		case "is_virtual":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			v := req.Msg.GetIsVirtual()
			co.IsVirtual = &v
		case "is_commute":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			v := req.Msg.GetIsCommute()
			co.IsCommute = &v
		case "is_race":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			v := req.Msg.GetIsRace()
			co.IsRace = &v
		case "is_ebike":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			v := req.Msg.GetIsEbike()
			co.IsEbike = &v
		case "custom_subtype":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			cs := req.Msg.GetCustomSubtype()
			if cs == "" {
				co.CustomSubtype = nil
			} else {
				co.CustomSubtype = &cs
			}
		case "distance_m":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			if v := req.Msg.GetDistanceM(); v < 0 {
				co.DistanceM = nil // negative clears → revert to merged
			} else {
				co.DistanceM = &v
			}
		case "elevation_gain_m":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			if v := req.Msg.GetElevationGainM(); v < 0 {
				co.ElevationGainM = nil
			} else {
				co.ElevationGainM = &v
			}
		case "moving_duration_s":
			if s.ClassOverrides == nil {
				return nil, rejectClass(f)
			}
			co, err := ensureCO()
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
			}
			if v := req.Msg.GetMovingDurationS(); v < 0 {
				co.MovingDuration = nil
			} else {
				d := time.Duration(v) * time.Second
				co.MovingDuration = &d
			}
		case "primary_stream_source_id":
			// Merge-managed (FieldGroupGPSTrack); changed via a gps_track field
			// pin, not a direct edit.
			return nil, rejectClass(f)
		default:
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
				fmt.Errorf("unknown field %q in field_mask", f))
		}
	}

	if classTouched {
		if err := s.ClassOverrides.Set(ctx, *classOverride); err != nil {
			return nil, connectrpc.NewError(connectrpc.CodeInternal,
				fmt.Errorf("save classification override: %w", err))
		}
	}

	if err := s.Activities.SaveActivity(ctx, activity); err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("save activity: %w", err))
	}

	// Re-merge from all sources. preserveUserEdits copies the just-saved
	// user fields onto the merged result, so the edit sticks.
	if _, err := s.Recompute.Execute(ctx, activityID); err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("recompute activity: %w", err))
	}

	updated, err := s.Activities.GetActivity(ctx, activityID)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("reload activity: %w", err))
	}

	// Federate a visibility transition: making an activity public announces it
	// to remote followers (the ingest-time publish only covers public-at-import);
	// taking it back out of public retracts it. Best-effort, never fails the edit.
	if s.Federation != nil && privacyTouched && prevPrivacy != updated.Privacy {
		switch {
		case updated.Privacy == domain.PrivacyPublic:
			s.Federation.PublishCreate(ctx, updated)
		case prevPrivacy == domain.PrivacyPublic:
			s.Federation.PublishDelete(ctx, updated.UserID, activityID)
		}
	}

	sources, err := s.Activities.ListSourcesForActivity(ctx, activityID)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list sources: %w", err))
	}

	return connectrpc.NewResponse(&v1.UpdateActivityResponse{
		Activity: activityToProto(updated, sources),
	}), nil
}

// ---------------------------------------------------------------------------
// GetActivityStream
//
// Reads time-series samples for a single source. The repo chooses the
// resolution: raw, 5s-CAgg or 30s-CAgg based on the request's
// max_resolution_hz hint. When 0, we default to 5s — the right tradeoff
// for typical activity-page rendering.
// ---------------------------------------------------------------------------

func (s *ActivityServer) GetActivityStream(
	ctx context.Context,
	req *connectrpc.Request[v1.GetActivityStreamRequest],
) (*connectrpc.Response[v1.GetActivityStreamResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	sourceUUID, err := uuid.Parse(req.Msg.GetActivitySourceId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("invalid activity_source_id"))
	}
	sourceID := domain.SourceID(sourceUUID)

	// Ownership check via the source's parent activity. We don't have a
	// direct user_id on ActivitySource, so we look up the source then
	// its activity.
	src, err := s.Activities.GetSource(ctx, sourceID)
	if err != nil {
		return nil, notFoundErr(err)
	}
	activity, err := s.Activities.GetActivity(ctx, src.ActivityID)
	if err != nil {
		return nil, notFoundErr(err)
	}
	if activity.UserID != userID {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound,
			errors.New("not found"))
	}

	stream, err := s.Streams.QueryStream(ctx, domain.StreamQuery{
		ActivitySourceID: sourceID,
		Channels:         channelsFromProto(req.Msg.GetChannels()),
		Resolution:       chooseResolution(req.Msg.GetMaxResolutionHz()),
	})
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("query stream: %w", err))
	}

	return connectrpc.NewResponse(&v1.GetActivityStreamResponse{
		Stream: streamToProto(stream),
	}), nil
}

// ---------------------------------------------------------------------------
// ListBestEfforts
//
// Returns every sliding-window peak computed for the activity, optionally
// filtered to a subset of BestEffortMetric values. Activity ownership is
// enforced via the parent activity's user_id.
// ---------------------------------------------------------------------------

func (s *ActivityServer) ListBestEfforts(
	ctx context.Context,
	req *connectrpc.Request[v1.ListBestEffortsRequest],
) (*connectrpc.Response[v1.ListBestEffortsResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Msg.GetActivityId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("invalid activity_id"))
	}
	activityID := domain.ActivityID(id)

	// Ownership check — load the activity to verify the caller owns it.
	activity, err := s.Activities.GetActivity(ctx, activityID)
	if err != nil {
		return nil, notFoundErr(err)
	}
	if activity.UserID != userID || activity.IsDeleted() {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound, errors.New("not found"))
	}

	efforts, err := s.BestEfforts.ListForActivity(ctx, activityID)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list best efforts: %w", err))
	}

	// Optional metric filter. UNSPECIFIED in the input list is dropped;
	// an empty post-filter set means "no filter".
	allowed := metricFilter(req.Msg.GetMetrics())

	out := &v1.ListBestEffortsResponse{
		Efforts: make([]*v1.BestEffort, 0, len(efforts)),
	}
	for _, e := range efforts {
		if allowed != nil {
			if _, ok := allowed[e.Metric]; !ok {
				continue
			}
		}
		out.Efforts = append(out.Efforts, bestEffortToProto(e))
	}
	return connectrpc.NewResponse(out), nil
}

// metricFilter converts the proto repeated-enum filter into a domain set.
// Returns nil when no filter was requested.
func metricFilter(ms []v1.BestEffortMetric) map[domain.BestEffortMetric]struct{} {
	if len(ms) == 0 {
		return nil
	}
	out := make(map[domain.BestEffortMetric]struct{}, len(ms))
	for _, m := range ms {
		if dm, ok := bestEffortMetricFromProto(m); ok {
			out[dm] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers local to this file
// ---------------------------------------------------------------------------

// windowFromRequest maps the proto TimeRange to (start, end). An empty
// range defaults to "last 90 days" so the frontend feed has something
// to show on a fresh login.
func windowFromRequest(tr *v1.TimeRange) (start, end time.Time) {
	now := time.Now().UTC()
	if tr == nil || (tr.GetFrom() == nil && tr.GetTo() == nil) {
		return now.Add(-defaultListWindow), now
	}
	if s := tr.GetFrom(); s != nil {
		start = s.AsTime()
	} else {
		start = time.Unix(0, 0)
	}
	if e := tr.GetTo(); e != nil {
		end = e.AsTime()
	} else {
		end = now
	}
	return start, end
}

// chooseResolution picks a CAgg resolution from the request's hint. The
// hint is a target sample rate in Hz; storage offers 1Hz raw, 0.2Hz 5s,
// 0.033Hz 30s. We pick the *coarsest* resolution that meets the hint —
// fewer rows over the wire is the goal.
func chooseResolution(maxHz float64) domain.StreamResolution {
	switch {
	case maxHz <= 0:
		return domain.StreamResolution5s
	case maxHz >= 1.0:
		return domain.StreamResolutionRaw
	case maxHz >= 0.2:
		return domain.StreamResolution5s
	default:
		return domain.StreamResolution30s
	}
}

// reverseActivities reverses the slice in place. Used to flip the repo's
// ASC order to the proto-default DESC.
func reverseActivities(xs []*v1.Activity) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}
