package connect

import (
	"context"
	"errors"
	"fmt"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/opencairn-org/cairn-core/gen/proto/cairn/v1"
	"github.com/opencairn-org/cairn-core/gen/proto/cairn/v1/cairnv1connect"
	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/protoconv"
	gearuc "github.com/opencairn-org/cairn-core/internal/usecase/gear"
)

// GearServer implements cairnv1connect.GearServiceHandler over the gear use
// cases. Gear is owner-only: every lookup is scoped to the caller.
type GearServer struct {
	cairnv1connect.UnimplementedGearServiceHandler

	Gear *gearuc.Service
	// Sync publishes an import_gear job for one of the caller's accounts.
	// queued=false with a nil error means no connected worker for the
	// provider imports gear. Nil when no job bus is wired.
	Sync func(ctx context.Context, userID domain.UserID, accountID domain.ExternalAccountID) (queued bool, err error)
}

var _ cairnv1connect.GearServiceHandler = (*GearServer)(nil)

func NewGearServer(gear *gearuc.Service, sync func(context.Context, domain.UserID, domain.ExternalAccountID) (bool, error)) *GearServer {
	return &GearServer{Gear: gear, Sync: sync}
}

func (s *GearServer) ListGear(ctx context.Context, req *v1.ListGearRequest) (*v1.ListGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	gear, err := s.Gear.List(ctx, userID, req.GetIncludeRetired())
	if err != nil {
		return nil, connectError(connectrpc.CodeInternal, fmt.Errorf("list gear: %w", err))
	}
	out := &v1.ListGearResponse{Gear: make([]*v1.Gear, 0, len(gear))}
	for _, g := range gear {
		out.Gear = append(out.Gear, gearToProto(g))
	}
	return out, nil
}

func (s *GearServer) GetGear(ctx context.Context, req *v1.GetGearRequest) (*v1.GetGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	id, err := gearIDFromString(req.GetId())
	if err != nil {
		return nil, err
	}
	g, err := s.Gear.Get(ctx, userID, id)
	if err != nil {
		return nil, gearErr(err)
	}
	return &v1.GetGearResponse{Gear: gearToProto(g)}, nil
}

func (s *GearServer) CreateGear(ctx context.Context, req *v1.CreateGearRequest) (*v1.CreateGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	purchase, err := purchaseDateFromString(req.GetPurchaseDate())
	if err != nil {
		return nil, err
	}
	g, err := s.Gear.Create(ctx, userID, gearuc.CreateInput{
		Kind:         protoconv.GearKindFromProto(req.GetKind()),
		Name:         req.GetName(),
		Brand:        req.GetBrand(),
		Model:        req.GetModel(),
		Notes:        req.GetNotes(),
		PurchaseDate: purchase,
	})
	if err != nil {
		return nil, gearErr(err)
	}
	return &v1.CreateGearResponse{Gear: gearToProto(g)}, nil
}

func (s *GearServer) UpdateGear(ctx context.Context, req *v1.UpdateGearRequest) (*v1.UpdateGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	id, err := gearIDFromString(req.GetId())
	if err != nil {
		return nil, err
	}
	if len(req.GetFieldMask()) == 0 {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "field_mask must list at least one field to update")
	}
	var purchase *time.Time
	for _, f := range req.GetFieldMask() {
		switch f {
		case "name", "brand", "model", "notes", "kind", "retired":
		case "purchase_date":
			if purchase, err = purchaseDateFromString(req.GetPurchaseDate()); err != nil {
				return nil, err
			}
		default:
			return nil, connectError(connectrpc.CodeInvalidArgument, fmt.Errorf("unknown field %q in field_mask", f))
		}
	}
	g, err := s.Gear.Update(ctx, userID, id, func(g *domain.Gear) {
		for _, f := range req.GetFieldMask() {
			switch f {
			case "name":
				g.Name = req.GetName()
			case "brand":
				g.Brand = req.GetBrand()
			case "model":
				g.Model = req.GetModel()
			case "notes":
				g.Notes = req.GetNotes()
			case "purchase_date":
				g.PurchaseDate = purchase
			case "kind":
				g.Kind = protoconv.GearKindFromProto(req.GetKind())
			case "retired":
				g.Retired = req.GetRetired()
			}
		}
	})
	if err != nil {
		return nil, gearErr(err)
	}
	return &v1.UpdateGearResponse{Gear: gearToProto(g)}, nil
}

func (s *GearServer) DeleteGear(ctx context.Context, req *v1.DeleteGearRequest) (*v1.DeleteGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	id, err := gearIDFromString(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.Gear.Delete(ctx, userID, id); err != nil {
		return nil, gearErr(err)
	}
	return &v1.DeleteGearResponse{}, nil
}

func (s *GearServer) MergeGear(ctx context.Context, req *v1.MergeGearRequest) (*v1.MergeGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	from, err := gearIDFromString(req.GetFromId())
	if err != nil {
		return nil, err
	}
	to, err := gearIDFromString(req.GetToId())
	if err != nil {
		return nil, err
	}
	g, err := s.Gear.Merge(ctx, userID, from, to)
	if err != nil {
		return nil, gearErr(err)
	}
	return &v1.MergeGearResponse{Gear: gearToProto(g)}, nil
}

func (s *GearServer) LinkGearRef(ctx context.Context, req *v1.LinkGearRefRequest) (*v1.LinkGearRefResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	id, err := gearIDFromString(req.GetGearId())
	if err != nil {
		return nil, err
	}
	acct, err := uuid.Parse(req.GetExternalAccountId())
	if err != nil || req.GetProvider() == "" || req.GetExternalId() == "" {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "provider, external_account_id and external_id are required")
	}
	g, err := s.Gear.LinkRef(ctx, userID, id, domain.GearRefKey{
		Provider:          req.GetProvider(),
		ExternalAccountID: domain.ExternalAccountID(acct),
		ExternalID:        req.GetExternalId(),
	})
	if err != nil {
		return nil, gearErr(err)
	}
	return &v1.LinkGearRefResponse{Gear: gearToProto(g)}, nil
}

func (s *GearServer) SetGearDefault(ctx context.Context, req *v1.SetGearDefaultRequest) (*v1.SetGearDefaultResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	t, ok := activityTypeFromProto(req.GetActivityType())
	if !ok {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "activity_type is required")
	}
	id, err := gearIDFromString(req.GetGearId())
	if err != nil {
		return nil, err
	}
	if err := s.Gear.SetDefault(ctx, userID, t, id); err != nil {
		return nil, gearErr(err)
	}
	return &v1.SetGearDefaultResponse{}, nil
}

func (s *GearServer) ClearGearDefault(ctx context.Context, req *v1.ClearGearDefaultRequest) (*v1.ClearGearDefaultResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	t, ok := activityTypeFromProto(req.GetActivityType())
	if !ok {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "activity_type is required")
	}
	if err := s.Gear.ClearDefault(ctx, userID, t); err != nil {
		return nil, gearErr(err)
	}
	return &v1.ClearGearDefaultResponse{}, nil
}

func (s *GearServer) ListGearDefaults(ctx context.Context, _ *v1.ListGearDefaultsRequest) (*v1.ListGearDefaultsResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	defaults, err := s.Gear.ListDefaults(ctx, userID)
	if err != nil {
		return nil, connectError(connectrpc.CodeInternal, fmt.Errorf("list gear defaults: %w", err))
	}
	out := &v1.ListGearDefaultsResponse{Defaults: make([]*v1.GearDefault, 0, len(defaults))}
	for _, d := range defaults {
		out.Defaults = append(out.Defaults, &v1.GearDefault{
			ActivityType: activityTypeToProto(d.ActivityType),
			GearId:       d.GearID.String(),
		})
	}
	return out, nil
}

func (s *GearServer) SyncGear(ctx context.Context, req *v1.SyncGearRequest) (*v1.SyncGearResponse, error) {
	userID, err := userIDFromHandlerCtx(ctx)
	if err != nil {
		return nil, err
	}
	if s.Sync == nil {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, "worker bus unavailable")
	}
	raw, err := uuid.Parse(req.GetExternalAccountId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "invalid external_account_id")
	}
	queued, err := s.Sync(ctx, userID, domain.ExternalAccountID(raw))
	if err != nil {
		return nil, gearErr(err)
	}
	if !queued {
		return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, "no connected worker for this provider imports gear")
	}
	return &v1.SyncGearResponse{Queued: true}, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func gearIDFromString(raw string) (domain.GearID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return domain.GearID{}, connectrpc.NewError(connectrpc.CodeInvalidArgument, "invalid gear id")
	}
	return domain.GearID(id), nil
}

// purchaseDateFromString parses an ISO date; empty clears it.
func purchaseDateFromString(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "purchase_date must be YYYY-MM-DD")
	}
	return &t, nil
}

// gearErr maps use-case errors: another user's gear reads as not found,
// invariant violations are the caller's fault.
func gearErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, gearuc.ErrNotOwner):
		return connectError(connectrpc.CodeNotFound, domain.ErrNotFound)
	case errors.Is(err, domain.ErrGearMissingName), errors.Is(err, domain.ErrGearInvalidKind),
		errors.Is(err, domain.ErrGearSelfMerge), errors.Is(err, domain.ErrActivityInvalidType):
		return connectError(connectrpc.CodeInvalidArgument, err)
	}
	return connectError(connectrpc.CodeInternal, err)
}

func gearToProto(g domain.Gear) *v1.Gear {
	out := &v1.Gear{
		Id:             g.ID.String(),
		Kind:           protoconv.GearKindToProto(g.Kind),
		Name:           g.Name,
		Brand:          g.Brand,
		Model:          g.Model,
		Notes:          g.Notes,
		Retired:        g.Retired,
		TotalDistanceM: g.TotalDistanceM,
		TotalMovingS:   int64(g.TotalMoving / time.Second),
		ActivityCount:  g.ActivityCount,
		CreatedAt:      timestamppb.New(g.CreatedAt),
		UpdatedAt:      timestamppb.New(g.UpdatedAt),
	}
	if g.PurchaseDate != nil {
		out.PurchaseDate = g.PurchaseDate.Format("2006-01-02")
	}
	if g.RetiredAt != nil {
		out.RetiredAt = timestamppb.New(*g.RetiredAt)
	}
	for _, r := range g.ExternalRefs {
		out.ExternalRefs = append(out.ExternalRefs, &v1.GearExternalRef{
			Provider:          r.Provider,
			ExternalAccountId: r.ExternalAccountID.String(),
			ExternalId:        r.ExternalID,
			ProviderName:      r.ProviderName,
			ProviderDistanceM: r.ProviderDistanceM,
			Primary:           r.Primary,
			LastSeenAt:        timestamppb.New(r.LastSeenAt),
		})
	}
	return out
}
