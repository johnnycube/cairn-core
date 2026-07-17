package connect

import (
	"context"
	"fmt"

	connectrpc "connectrpc.com/connect"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/gen/proto/cairn/v1/cairnv1connect"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// AdminServer implements cairnv1connect.AdminServiceHandler.
//
// Implemented today: ListUsers, ListOIDCClients (read-only). Every other RPC
// falls through to the Unimplemented base and returns CodeUnimplemented.
//
// OIDC providers are configured via CAIRN_OIDC_* env vars, so the admin
// surface is read-only — there is no create/update/delete.
//
// Authorisation: all RPCs require the caller to have `admin` role. The
// check is performed by RequireAdminInterceptor when this service is
// mounted; handlers themselves only do the standard userIDFromHandlerCtx
// for traceability — the interceptor has already filtered non-admins.
type AdminServer struct {
	cairnv1connect.UnimplementedAdminServiceHandler

	Users port.UserRepo
	// OIDCProviders is the env-configured (CAIRN_OIDC_*) provider list,
	// surfaced read-only by ListOIDCClients.
	OIDCProviders []domain.OIDCProvider
}

var _ cairnv1connect.AdminServiceHandler = (*AdminServer)(nil)

func NewAdminServer(users port.UserRepo, providers []domain.OIDCProvider) *AdminServer {
	return &AdminServer{Users: users, OIDCProviders: providers}
}

// ---------------------------------------------------------------------------
// ListUsers
// ---------------------------------------------------------------------------

func (s *AdminServer) ListUsers(
	ctx context.Context,
	req *connectrpc.Request[v1.ListUsersRequest],
) (*connectrpc.Response[v1.ListUsersResponse], error) {
	limit, _ := pageFrom(req.Msg.GetPage())
	cursor := ""
	if p := req.Msg.GetPage(); p != nil {
		cursor = p.GetCursor()
	}

	users, nextCursor, err := s.Users.ListUsers(ctx, port.UserListPage{
		Cursor:   cursor,
		PageSize: limit,
	})
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list users: %w", err))
	}

	out := &v1.ListUsersResponse{
		Users: make([]*v1.AdminUserSummary, 0, len(users)),
		Page:  &v1.PageResponse{NextCursor: nextCursor, Total: -1},
	}
	for _, u := range users {
		// activity_count / external_account_count / storage_bytes_used
		// require dedicated counting queries which the v1 admin page
		// doesn't strictly need. Returning zero is honest — the proto
		// allows it.
		out.Users = append(out.Users, &v1.AdminUserSummary{User: userToProto(u)})
	}
	return connectrpc.NewResponse(out), nil
}

// ---------------------------------------------------------------------------
// ListOIDCClients
// ---------------------------------------------------------------------------

func (s *AdminServer) ListOIDCClients(
	ctx context.Context,
	req *connectrpc.Request[v1.ListOIDCClientsRequest],
) (*connectrpc.Response[v1.ListOIDCClientsResponse], error) {
	out := &v1.ListOIDCClientsResponse{}
	for _, p := range s.OIDCProviders {
		out.Clients = append(out.Clients, oidcProviderToProto(p))
	}
	return connectrpc.NewResponse(out), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func oidcProviderToProto(p domain.OIDCProvider) *v1.OIDCClient {
	return &v1.OIDCClient{
		Id:                p.ID,
		DisplayName:       p.Name,
		IssuerUrl:         p.IssuerURL,
		ClientId:          p.ClientID,
		HasClientSecret:   p.ClientSecret != "",
		Scopes:            p.Scopes,
		UsePkce:           p.UsePKCE,
		Audience:          p.Audience,
		SkipAudienceCheck: p.SkipAudienceCheck,
		AutoProvision:     p.AutoProvision,
		AutoProvisionRole: userRoleToProto(p.AutoProvisionRole),
	}
}
