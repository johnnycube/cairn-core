package protoconv

import (
	cairnv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	workerv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/worker/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// ClimbCategoryFromProto maps the wire enum to the domain value.
func ClimbCategoryFromProto(c cairnv1.ClimbCategory) domain.ClimbCategory {
	switch c {
	case cairnv1.ClimbCategory_CLIMB_CATEGORY_FOUR:
		return domain.ClimbCategory4
	case cairnv1.ClimbCategory_CLIMB_CATEGORY_THREE:
		return domain.ClimbCategory3
	case cairnv1.ClimbCategory_CLIMB_CATEGORY_TWO:
		return domain.ClimbCategory2
	case cairnv1.ClimbCategory_CLIMB_CATEGORY_ONE:
		return domain.ClimbCategory1
	case cairnv1.ClimbCategory_CLIMB_CATEGORY_HORS_CATEGORIE:
		return domain.ClimbCategoryHorsCategorie
	default:
		return domain.ClimbCategoryNone
	}
}

// SegmentFromProto maps an imported (external, provider-mirrored) segment to the
// domain shape. The ID is left zero — the ingest path resolves an existing
// segment (find-or-create) and assigns it. externalID + accountID come from the
// event's ExternalRef.
func SegmentFromProto(
	p *workerv1.SegmentImport,
	externalID string,
	accountID domain.ExternalAccountID,
) domain.Segment {
	ext := externalID
	acc := accountID
	return domain.Segment{
		Source:            domain.SegmentSourceExternal,
		ExternalID:        &ext,
		ExternalAccountID: &acc,
		Scope:             domain.SegmentScopePrivate, // external segments are always private
		Name:              p.GetName(),
		Description:       p.GetDescription(),
		ActivityType:      ActivityTypeFromProto(p.GetActivityType()),
		Polyline:          p.GetEncodedPolyline(),
		PolylinePrecision: 5,
		DistanceM:         p.GetDistanceM(),
		ElevationGainM:    p.ElevationGainM,
		ElevationLossM:    p.ElevationLossM,
		AvgGrade:          p.AvgGrade,
		MaxGrade:          p.MaxGrade,
		ClimbCategory:     ClimbCategoryFromProto(p.GetClimbCategory()),
		Bidirectional:     p.GetBidirectional(),
		Starred:           p.GetStarred(),
	}
}

// SegmentEffortFromProto maps an imported segment effort's payload to the domain
// shape. The ingest path fills the resolved SegmentID/ActivityID/SourceID/UserID
// and a generated ID; providerEffortID comes from the event's ExternalRef.
func SegmentEffortFromProto(p *workerv1.SegmentEffortImport, providerEffortID string) domain.SegmentEffort {
	e := domain.SegmentEffort{
		StartTime:       p.GetStartTime().AsTime(),
		ElapsedS:        p.GetElapsed().AsDuration().Seconds(),
		MovingS:         p.GetMoving().AsDuration().Seconds(),
		StartOffset:     int(p.GetStartOffset()),
		EndOffset:       int(p.GetEndOffset()),
		AvgHeartRateBpm: i32ToIntPtr(p.AvgHeartRateBpm),
		MaxHeartRateBpm: i32ToIntPtr(p.MaxHeartRateBpm),
		AvgPowerW:       i32ToIntPtr(p.AvgPowerW),
		AvgCadence:      i32ToIntPtr(p.AvgCadence),
		AvgSpeedMps:     p.AvgSpeedMps,
	}
	if providerEffortID != "" {
		e.ProviderEffortExternalID = &providerEffortID
	}
	return e
}
