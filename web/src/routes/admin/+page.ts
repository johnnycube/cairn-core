import { error, redirect } from '@sveltejs/kit';
import { connectClients, isUnauthenticated, Code, ConnectError } from '$lib/connect';
import { protoTimestampToISO } from '$lib/proto-helpers';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url, parent }) => {
	const { user, permissions } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	if (!permissions.includes('admin')) {
		error(403, 'Admin only');
	}

	const clients = connectClients(fetch, url.origin);
	try {
		const res = await clients.admin.listUsers({
			page: { cursor: '', limit: 100 }
		});
		return {
			users: res.users.map(userSummaryToView)
		};
	} catch (err) {
		if (isUnauthenticated(err)) {
			redirect(302, '/login');
		}
		if (err instanceof ConnectError && err.code === Code.PermissionDenied) {
			error(403, 'Admin only');
		}
		error(500, (err as Error).message);
	}
};

interface AdminUserView {
	id: string;
	username: string;
	email: string;
	displayName: string;
	role: string;
	status: string;
	createdAt: string;
	activityCount: number;
}

function userSummaryToView(s: any): AdminUserView {
	const u = s.user;
	return {
		id: u.id,
		username: u.username,
		email: u.email,
		displayName: u.displayName,
		role: u.role === 2 ? 'admin' : 'user',
		status: u.status === 1 ? 'active' : `status_${u.status}`,
		createdAt: protoTimestampToISO(u.createdAt),
		activityCount: s.activityCount ?? 0
	};
}
