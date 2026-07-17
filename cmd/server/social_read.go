package main

import (
	"context"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// social_read.go centralises the multi-user read-path: resolving a viewer's
// visibility against an activity owner, and projecting an activity to JSON
// through the resulting CategorySet so non-owner reads never leak gated data.

// resolveViewer collects the (optional) session user. ok=false means anonymous.
func optionalSessionUser(r *http.Request, app *App) (domain.UserID, bool) {
	return resolveSessionUser(r, app)
}

// visibilityFor computes the CategorySet a viewer may see for one owner's
// activity. viewerID is nil for anonymous viewers; hasLink is true when a valid
// share token was presented.
func visibilityFor(
	ctx context.Context,
	app *App,
	ownerID domain.UserID,
	viewerID *domain.UserID,
	activityID domain.ActivityID,
	hasLink bool,
) (domain.CategorySet, error) {
	in := domain.VisibilityInput{
		IsOwner:      viewerID != nil && *viewerID == ownerID,
		HasValidLink: hasLink,
	}
	if in.IsOwner {
		return domain.ResolveVisibility(in), nil
	}
	// A block (either direction) hides everything from the other party.
	if viewerID != nil && app.Blocks != nil {
		if blocked, err := app.Blocks.IsBlockedEitherWay(ctx, *viewerID, ownerID); err == nil && blocked {
			return domain.CategorySet{}, nil
		}
	}
	// Follower?
	if viewerID != nil && app.Follows != nil {
		if following, err := app.Follows.IsFollowing(ctx, *viewerID, ownerID); err == nil {
			in.IsFollower = following
		}
	}
	if app.Visibility != nil {
		if p, err := app.Visibility.GetUserPolicy(ctx, ownerID); err == nil {
			in.OwnerPolicy = p
		}
		if ov, err := app.Visibility.GetActivityOverride(ctx, activityID); err == nil {
			in.ActivityPolicy = ov
		}
	}
	return domain.ResolveVisibility(in), nil
}

// ownerPrivacyZones loads the owner's privacy zones to apply to a NON-owner
// projection. Returns nil for owner reads (an owner always sees their own
// coordinates) or when zones can't be loaded — fail-open on the load error but
// fail-closed on a positive zone match (see projectActivityJSON).
func ownerPrivacyZones(ctx context.Context, app *App, ownerID domain.UserID, isOwner bool) []domain.PrivacyZone {
	if isOwner || app.Visibility == nil {
		return nil
	}
	zones, err := app.Visibility.ListPrivacyZones(ctx, ownerID)
	if err != nil {
		return nil
	}
	return zones
}

// projectActivityJSON emits an activity as JSON limited to the allowed
// categories. Summary fields are always present (the lowest tier). Map and
// location-bearing fields are only included when their category is allowed.
//
// zones are the owner's privacy zones (nil for owner reads). When the
// activity's start point falls inside any zone, the start coordinates AND the
// resolved place name are suppressed even if CategoryLocation is granted — a
// home/work geofence the owner configured. visible_categories still lists
// location so the client can render "start hidden", flagged by
// start_location_redacted.
func projectActivityJSON(a domain.Activity, cats domain.CategorySet, zones []domain.PrivacyZone) map[string]any {
	out := map[string]any{
		"id":                 a.ID.String(),
		"title":              a.Title,
		"type":               string(a.Type),
		"discipline":         string(a.Discipline),
		"start_time":         a.StartTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"timezone":           a.Timezone,
		"elapsed_duration_s": int64(a.ElapsedDuration.Seconds()),
		"moving_duration_s":  int64(a.MovingDuration.Seconds()),
		"distance_m":         a.Summary.DistanceM,
		"elevation_gain_m":   a.Summary.ElevationGainM,
	}
	if cats.Has(domain.CategorySummary) {
		out["description"] = a.Description
		out["tss"] = a.Summary.TSS
		out["calories"] = a.Summary.CaloriesKcal
	}
	if cats.Has(domain.CategoryLocation) {
		inZone := a.StartLat != nil && a.StartLng != nil &&
			domain.PointInAnyZone(zones, *a.StartLat, *a.StartLng)
		if inZone {
			// Geofenced: reveal neither the coordinates nor the place name.
			out["start_location_redacted"] = true
		} else {
			out["start_place"] = a.StartPlace
			if a.StartLat != nil && a.StartLng != nil {
				out["start_lat"] = *a.StartLat
				out["start_lng"] = *a.StartLng
			}
		}
	}
	if cats.Has(domain.CategoryHR) {
		out["avg_hr"] = a.Summary.AvgHeartRateBpm
		out["max_hr"] = a.Summary.MaxHeartRateBpm
	}
	if cats.Has(domain.CategoryPower) {
		out["avg_power"] = a.Summary.AvgPowerW
		out["max_power"] = a.Summary.MaxPowerW
		out["normalized_power"] = a.Summary.NormalizedPowerW
	}
	if cats.Has(domain.CategoryPaceSpeed) {
		out["avg_speed_mps"] = a.Summary.AvgSpeedMps
		out["max_speed_mps"] = a.Summary.MaxSpeedMps
	}
	// When the viewer may see the map, expose the static course-image URL (the
	// endpoint re-checks visibility + trims privacy zones). This is the
	// non-owner map surface — feeds, lists, and federation use it instead of an
	// interactive map (no raw GPS track is ever served to non-owners).
	if cats.Has(domain.CategoryMap) {
		out["map_image_url"] = "/api/activities/" + a.ID.String() + "/map.png"
	}
	// When photos are shared, point at the attachments list endpoint, which
	// re-checks visibility and serves each image via its own gated /raw URL.
	if cats.Has(domain.CategoryPhotos) {
		out["photos_url"] = "/api/activities/" + a.ID.String() + "/attachments"
	}
	// The category list tells the client which richer views (map, segments,
	// best-efforts, kudos/comments) it may request.
	allowed := make([]string, 0, len(cats))
	for _, c := range domain.AllCategories {
		if cats.Has(c) {
			allowed = append(allowed, string(c))
		}
	}
	out["visible_categories"] = allowed
	return out
}
