import { createClient, Code, ConnectError } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';

import { ActivityService } from '$proto/cairn/v1/activity_pb.js';
import { SegmentService } from '$proto/cairn/v1/segment_pb.js';
import { MetricsService } from '$proto/cairn/v1/metrics_pb.js';
import { NotificationService } from '$proto/cairn/v1/notification_pb.js';
import { AdminService } from '$proto/cairn/v1/admin_pb.js';
import { AuthService, ExternalAccountService } from '$proto/cairn/v1/auth_pb.js';

// Browser-side Connect-RPC clients.
//
// The app is a static SPA (ssr=false in the root +layout.ts), so these run
// only in the browser and hit the same origin: the cairn_session cookie
// rides along automatically (fetch's default same-origin credentials). In
// dev the origin is the Caddy proxy; in prod it's the Go binary serving the
// embedded SPA. Both route /cairn.v1.* to the Go server.

function originFallback(): string {
	return typeof window !== 'undefined' ? window.location.origin : '';
}

// connectClients builds a client bundle bound to a specific fetch + origin.
// Universal loads pass the load event's `fetch` (so SvelteKit can track the
// request) and `url.origin`. Elsewhere the defaults — the global fetch and
// the current window origin — are fine.
export function connectClients(fetchFn: typeof fetch = fetch, baseUrl?: string) {
	const transport = createConnectTransport({
		baseUrl: baseUrl ?? originFallback(),
		fetch: fetchFn
	});
	return {
		activity: createClient(ActivityService, transport),
		segment: createClient(SegmentService, transport),
		metrics: createClient(MetricsService, transport),
		notification: createClient(NotificationService, transport),
		admin: createClient(AdminService, transport),
		auth: createClient(AuthService, transport),
		externalAccount: createClient(ExternalAccountService, transport)
	};
}

// clients is a ready-made singleton for component-level calls (mutations,
// lazy fetches) that aren't part of a load.
export const clients = connectClients();

export { ConnectError, Code };

// isUnauthenticated detects the canonical "no session" path so callers can
// redirect to /login. The Go SessionInterceptor emits Unauthenticated for
// any caller without a valid session cookie.
export function isUnauthenticated(err: unknown): boolean {
	return err instanceof ConnectError && err.code === Code.Unauthenticated;
}
