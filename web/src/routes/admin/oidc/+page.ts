import { error, redirect } from '@sveltejs/kit';
import { connectClients, isUnauthenticated, Code, ConnectError } from '$lib/connect';
import type { OIDCClient } from '$proto/cairn/v1/admin_pb.js';
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
		const res = await clients.admin.listOIDCClients({});
		return { oidcClients: res.clients.map(clientToView) };
	} catch (err) {
		if (isUnauthenticated(err)) redirect(302, '/login');
		if (err instanceof ConnectError && err.code === Code.PermissionDenied) {
			error(403, 'Admin only');
		}
		error(500, (err as Error).message);
	}
};

export interface OIDCProviderView {
	id: string;
	displayName: string;
	issuerURL: string;
	clientID: string;
	hasClientSecret: boolean;
	scopes: string[];
	usePKCE: boolean;
	audience: string;
	skipAudienceCheck: boolean;
	autoProvision: boolean;
}

function clientToView(c: OIDCClient): OIDCProviderView {
	return {
		id: c.id,
		displayName: c.displayName,
		issuerURL: c.issuerUrl,
		clientID: c.clientId,
		hasClientSecret: c.hasClientSecret,
		scopes: c.scopes,
		usePKCE: c.usePkce,
		audience: c.audience,
		skipAudienceCheck: c.skipAudienceCheck,
		autoProvision: c.autoProvision
	};
}
