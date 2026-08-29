import { connectClients, isUnauthenticated } from '$lib/connect';
import { type User, UserRole, UnitSystem, DateFormat, TimeFormat } from '$proto/cairn/v1/auth_pb.js';
import type { LayoutLoad } from './$types';

// The whole app is a client-rendered SPA: prod ships as a static bundle
// embedded in the Go binary (no Node server), and dev matches that so the
// two render identically. Loads therefore run only in the browser.
export const ssr = false;
export const prerender = false;

// Resolves the currently-logged-in user once and threads it into every
// page via layout data. Unauthenticated visitors get `user: null` — pages
// render the public skeleton or redirect to /login as appropriate.
export interface InstanceFeatures {
	federation: boolean;
}

// Optional instance-level features (CAIRN_*_ENABLED flags). Best-effort and
// public; everything defaults to off so a failed fetch hides, never shows.
async function loadFeatures(fetch: typeof globalThis.fetch): Promise<InstanceFeatures> {
	const off: InstanceFeatures = { federation: false };
	try {
		const res = await fetch('/api/instance/features');
		if (!res.ok) return off;
		const f = (await res.json()) as Partial<InstanceFeatures>;
		return { ...off, federation: f.federation === true };
	} catch {
		return off;
	}
}

export const load: LayoutLoad = async ({ fetch, url }) => {
	const clients = connectClients(fetch, url.origin);
	const features = await loadFeatures(fetch);
	try {
		const res = await clients.auth.getCurrentUser({});
		// Best-effort unread count for the header bell; never fail the shell on it.
		let unreadCount = 0;
		try {
			const n = await clients.notification.listNotifications({
				onlyUnread: true,
				page: { cursor: '', limit: 50 }
			});
			unreadCount = n.notifications.length;
		} catch {
			/* ignore */
		}
		return {
			user: userToView(res.user),
			permissions: res.permissions ?? [],
			unreadCount,
			features
		};
	} catch (err) {
		if (isUnauthenticated(err)) {
			return { user: null, permissions: [], unreadCount: 0, features };
		}
		// Backend reachable but errored — render the shell anyway so a
		// transient hiccup doesn't take the whole UI down.
		console.warn('GetCurrentUser failed', err);
		return { user: null, permissions: [], unreadCount: 0, features };
	}
};

export interface UserView {
	id: string;
	username: string;
	email: string;
	emailVerified: boolean;
	displayName: string;
	role: 'user' | 'admin' | 'unknown';
	units: 'metric' | 'imperial';
	dateFormat: '' | 'iso' | 'us' | 'eu';
	timeFormat: '' | '24h' | '12h';
}

function dateFmt(d: DateFormat): UserView['dateFormat'] {
	switch (d) {
		case DateFormat.ISO:
			return 'iso';
		case DateFormat.US:
			return 'us';
		case DateFormat.EU:
			return 'eu';
		default:
			return '';
	}
}

function timeFmt(t: TimeFormat): UserView['timeFormat'] {
	switch (t) {
		case TimeFormat.TIME_FORMAT_24H:
			return '24h';
		case TimeFormat.TIME_FORMAT_12H:
			return '12h';
		default:
			return '';
	}
}

function userToView(u: User | undefined): UserView | null {
	if (!u) return null;
	return {
		id: u.id,
		username: u.username,
		email: u.email,
		emailVerified: u.emailVerified,
		displayName: u.displayName,
		role:
			u.role === UserRole.ADMIN
				? 'admin'
				: u.role === UserRole.USER
					? 'user'
					: 'unknown',
		units: u.units === UnitSystem.IMPERIAL ? 'imperial' : 'metric',
		dateFormat: dateFmt(u.dateFormat),
		timeFormat: timeFmt(u.timeFormat)
	};
}
