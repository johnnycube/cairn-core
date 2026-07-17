package main

import (
	"log/slog"
	"net/http"

	connectrpc "connectrpc.com/connect"

	"github.com/johnnycube/cairn-core/gen/proto/cairn/v1/cairnv1connect"
	connectadapter "github.com/johnnycube/cairn-core/internal/adapter/primary/connect"
)

// mountConnectRPC registers every cairn.v1.* Connect-RPC service the
// server currently implements onto the supplied mux.
//
// Service handlers come out of internal/adapter/primary/connect. Each
// embeds the generated UnimplementedXxxServiceHandler so the surface
// grows method-by-method; clients calling an unimplemented RPC get
// CodeUnimplemented, not a 404.
//
// Auth is currently header-based (X-Cairn-User-ID), set by the SvelteKit
// hooks.server.ts. The proper session-cookie / OIDC path replaces that
// header check once OIDC adapters land.
func mountConnectRPC(mux *http.ServeMux, app *App, logger *slog.Logger) {
	// Session interceptor: resolves the cairn_session cookie into a
	// context-stashed user id. Handlers read via userIDFromHandlerCtx;
	// the X-Cairn-User-ID header path stays as a fallback so the
	// SvelteKit dev proxy can keep working during the OIDC roll-out.
	sessionInterceptor := connectadapter.SessionInterceptor(app.Sessions, app.PATs, app.OAuth, sessionCookieName)
	// ScopeInterceptor keeps read-only OAuth tokens read-only; no-op for
	// cookie/PAT callers. Runs after the session resolver.
	opts := connectrpc.WithInterceptors(sessionInterceptor, connectadapter.ScopeInterceptor())

	activitySvc := connectadapter.NewActivityServer(app.Activities, app.Streams, app.BestEfforts, app.RecomputeActivity,
		app.ClassOverrides, newFederationPublisher(app, logger))
	{
		path, handler := cairnv1connect.NewActivityServiceHandler(activitySvc, opts)
		mux.Handle(path, handler)
		logger.Info("connect-rpc handler mounted", "service", "cairn.v1.ActivityService", "path", path)
	}

	notificationSvc := connectadapter.NewNotificationServer(app.Notifications)
	{
		path, handler := cairnv1connect.NewNotificationServiceHandler(notificationSvc, opts)
		mux.Handle(path, handler)
		logger.Info("connect-rpc handler mounted", "service", "cairn.v1.NotificationService", "path", path)
	}

	authSvc := connectadapter.NewAuthServer(app.Users, app.OIDCProviders)
	{
		path, handler := cairnv1connect.NewAuthServiceHandler(authSvc, opts)
		mux.Handle(path, handler)
		logger.Info("connect-rpc handler mounted", "service", "cairn.v1.AuthService", "path", path)
	}

	adminSvc := connectadapter.NewAdminServer(app.Users, app.OIDCProviders)
	{
		// AdminService chains the session resolver with a role-check
		// interceptor. Non-admins get CodePermissionDenied before the
		// handler runs.
		adminOpts := connectrpc.WithInterceptors(
			sessionInterceptor,
			connectadapter.ScopeInterceptor(),
			connectadapter.RequireAdminInterceptor(app.Users),
		)
		path, handler := cairnv1connect.NewAdminServiceHandler(adminSvc, adminOpts)
		mux.Handle(path, handler)
		logger.Info("connect-rpc handler mounted", "service", "cairn.v1.AdminService", "path", path)
	}
}
