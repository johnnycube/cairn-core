package connect

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// userIDFromRequest extracts the authenticated user id from a request.
//
// The ONLY auth source is the user id stuffed into the request context by
// SessionInterceptor after verifying the cairn_session cookie. The frontend
// (a same-origin SPA) sends that cookie automatically, so no header-based
// fallback is needed.
//
// SECURITY: a previous version also accepted an `X-Cairn-User-ID` request
// header as a fallback identity. That was an authentication bypass — the
// reverse proxy does not strip the header, so any client could impersonate
// any user by setting it. It has been removed; do not reintroduce a
// client-controlled identity header.
func userIDFromRequest[T any](req *connectrpc.Request[T]) (domain.UserID, error) {
	if id, ok := userIDFromCtxValue(req); ok {
		return id, nil
	}
	return domain.UserID{}, connectrpc.NewError(
		connectrpc.CodeUnauthenticated,
		errors.New("not authenticated"),
	)
}

// notFoundErr is the canonical mapping for repo lookups that miss.
func notFoundErr(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return connectrpc.NewError(connectrpc.CodeNotFound, err)
	}
	return connectrpc.NewError(connectrpc.CodeInternal, err)
}

// ---------------------------------------------------------------------------
// SessionInterceptor
//
// A Connect-RPC interceptor that reads the cairn_session cookie, looks
// up the session row, verifies it's active, and stuffs the resolved
// user ID into the request context. Handlers then call
// userIDFromRequest which checks the context first.
//
// Unauthenticated requests pass through — the interceptor doesn't deny;
// it only enriches. The handler decides whether the operation needs
// auth and emits CodeUnauthenticated when userIDFromRequest finds
// nothing.
// ---------------------------------------------------------------------------

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
	// ctxKeyScopes holds []string of OAuth scopes when the caller authenticated
	// with an OAuth access token. Absent for cookie/PAT auth, which carry the
	// user's full access (nil scopes == unrestricted).
	ctxKeyScopes
)

// patTokenPrefix marks a personal access token; oauthTokenPrefix marks an
// OAuth 2.1 access token issued by Cairn's authorization server.
const (
	patTokenPrefix   = "cairn_pat_"
	oauthTokenPrefix = "cairn_at_"
)

// SessionInterceptor builds a connect.Interceptor that resolves the caller's
// identity from either the cairn_session cookie (browser) or an
// `Authorization: Bearer cairn_pat_…` personal access token (CLI/API), stashing
// the user id in the context. Touches last-seen/last-used best-effort. `pats`
// may be nil (PAT auth then disabled).
func SessionInterceptor(sessions port.SessionRepo, pats port.PATRepo, oauth port.OAuthServerRepo, sessionCookieName string) connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			// 1. Session cookie.
			if sessions != nil {
				if token := readSessionCookie(req.Header().Get("Cookie"), sessionCookieName); token != "" {
					hash := sha256.Sum256([]byte(token))
					if sess, err := sessions.GetByTokenHash(ctx, hash[:]); err == nil && sess.IsActive(time.Now().UTC()) {
						_ = sessions.TouchLastSeen(ctx, sess.ID)
						return next(context.WithValue(ctx, ctxKeyUserID, sess.UserID), req)
					}
				}
			}
			// Bearer credential: PAT or OAuth access token.
			tok := strings.TrimPrefix(req.Header().Get("Authorization"), "Bearer ")
			// 2. Personal access token (full user access).
			if pats != nil && strings.HasPrefix(tok, patTokenPrefix) {
				hash := sha256.Sum256([]byte(tok))
				if pat, err := pats.FindByTokenHash(ctx, hash[:]); err == nil && pat.IsValidAt(time.Now().UTC()) {
					_ = pats.TouchLastUsed(ctx, pat.ID)
					return next(context.WithValue(ctx, ctxKeyUserID, pat.UserID), req)
				}
			}
			// 3. OAuth 2.1 access token (scoped access; ScopeInterceptor enforces).
			if oauth != nil && strings.HasPrefix(tok, oauthTokenPrefix) {
				hash := sha256.Sum256([]byte(tok))
				if at, err := oauth.FindAccessToken(ctx, hash[:]); err == nil && at.IsValidAt(time.Now().UTC()) {
					ctx = context.WithValue(ctx, ctxKeyUserID, at.UserID)
					ctx = context.WithValue(ctx, ctxKeyScopes, domain.ParseScopes(at.Scope))
					return next(ctx, req)
				}
			}
			// Not authenticated; handlers decide whether to refuse.
			return next(ctx, req)
		}
	}
}

// ScopeInterceptor enforces OAuth scopes on the Connect API. It only applies to
// callers authenticated via an OAuth access token (cookie/PAT auth carries the
// user's full access and is unaffected). A mutating procedure requires the
// token to hold at least one write scope; otherwise the call is denied. This
// keeps read-only tokens — the default for agents — truly read-only.
//
// Mount AFTER SessionInterceptor so scopes are already in ctx.
func ScopeInterceptor() connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			scopes, isOAuth := ctx.Value(ctxKeyScopes).([]string)
			if !isOAuth {
				return next(ctx, req) // cookie/PAT: unrestricted
			}
			if isMutatingProcedure(req.Spec().Procedure) && !hasAnyWriteScope(scopes) {
				return nil, connectrpc.NewError(connectrpc.CodePermissionDenied,
					errors.New("this OAuth token lacks a write scope for this operation"))
			}
			return next(ctx, req)
		}
	}
}

// isMutatingProcedure classifies a Connect procedure ("/svc/Method") as a
// mutation unless its method name starts with a read verb.
func isMutatingProcedure(procedure string) bool {
	method := procedure
	if i := strings.LastIndexByte(procedure, '/'); i >= 0 {
		method = procedure[i+1:]
	}
	for _, verb := range []string{"Get", "List", "Search", "Watch", "Stream", "Export", "Resolve", "Check", "Count", "Lookup", "Fetch"} {
		if strings.HasPrefix(method, verb) {
			return false
		}
	}
	return true
}

func hasAnyWriteScope(scopes []string) bool {
	for _, s := range scopes {
		if domain.IsWriteScope(s) {
			return true
		}
	}
	return false
}

// userIDFromCtxValue reads the interceptor's stash. Returns (zero, false)
// when no session interceptor ran or the cookie was missing.
//
// We expose the same lookup by both a Request[T] wrapper (for ergonomic
// handler code) and by *connect.AnyRequest implicitly via the context
// chain: connectrpc.NewResponse keeps the request's ctx in scope.
func userIDFromCtxValue[T any](req *connectrpc.Request[T]) (domain.UserID, bool) {
	v := contextOf(req).Value(ctxKeyUserID)
	if v == nil {
		return domain.UserID{}, false
	}
	id, ok := v.(domain.UserID)
	if !ok {
		return domain.UserID{}, false
	}
	return id, true
}

// connect.Request[T] doesn't expose ctx directly — the interceptor stash
// rides on the http.Request context which Connect threads into handlers
// via the outer context.Context argument. Handlers receive that ctx
// independently and pass it to the repo layer; for the userIDFromRequest
// helper we extract via the connect-internal HTTP method.
//
// In practice we just check connect's request peer info — for now we
// return Background() and rely on the header fallback. The interceptor
// path is fully functional via the `userIDFromContext` function below,
// which handlers can call explicitly when they have the ctx in hand.
func contextOf[T any](req *connectrpc.Request[T]) context.Context {
	// Best-effort: Connect exposes the http.Request via Peer(), but not
	// the original context object directly. Handlers should prefer
	// userIDFromHandlerCtx(ctx, req) when they have ctx.
	return context.Background()
}

// userIDFromHandlerCtx is the primary helper for handlers to call. It
// reads the interceptor stash directly off the supplied ctx, then
// falls back to userIDFromRequest's header path.
//
// Handlers that have ctx in scope (every Connect handler does) should
// prefer this over userIDFromRequest.
func userIDFromHandlerCtx[T any](ctx context.Context, req *connectrpc.Request[T]) (domain.UserID, error) {
	if v := ctx.Value(ctxKeyUserID); v != nil {
		if id, ok := v.(domain.UserID); ok {
			return id, nil
		}
	}
	return userIDFromRequest(req)
}

// RequireAdminInterceptor refuses any RPC where the resolved user
// doesn't have the admin role. Mount this on services where every RPC
// is admin-only (AdminService). Must run after SessionInterceptor in
// the chain so the user id is already in ctx.
//
// Cost: one extra UserRepo.GetUser lookup per call. Cache later if it
// shows up on traces — admin endpoints are low-volume so the join is
// fine for now.
func RequireAdminInterceptor(users port.UserRepo) connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			v := ctx.Value(ctxKeyUserID)
			if v == nil {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated,
					errors.New("authentication required"))
			}
			uid, ok := v.(domain.UserID)
			if !ok {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated,
					errors.New("invalid session"))
			}
			u, err := users.GetUser(ctx, uid)
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated,
					errors.New("user not found"))
			}
			if u.Role != domain.UserRoleAdmin {
				return nil, connectrpc.NewError(connectrpc.CodePermissionDenied,
					errors.New("admin role required"))
			}
			return next(ctx, req)
		}
	}
}

// readSessionCookie parses the supplied Cookie header line for the named
// session cookie. Tolerates multiple cookies in one header and ignores
// quoting (browsers don't quote session cookie values).
func readSessionCookie(cookieHeader, name string) string {
	if cookieHeader == "" || name == "" {
		return ""
	}
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if eq := strings.IndexByte(part, '='); eq > 0 {
			if part[:eq] == name {
				return part[eq+1:]
			}
		}
	}
	return ""
}
