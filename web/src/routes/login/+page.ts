import { redirect } from '@sveltejs/kit';
import { connectClients } from '$lib/connect';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url, parent }) => {
	const { user } = await parent();
	if (user) {
		// Already authenticated — skip the login page.
		redirect(302, '/');
	}

	const clients = connectClients(fetch, url.origin);
	try {
		const res = await clients.auth.getLoginMethods({});
		return {
			passwordEnabled: res.passwordEnabled,
			webauthnEnabled: res.webauthnEnabled,
			oidcClients: res.oidcClients.map((c) => ({
				id: c.id,
				displayName: c.displayName
			}))
		};
	} catch (err) {
		console.warn('GetLoginMethods failed', err);
		return {
			passwordEnabled: false,
			webauthnEnabled: false,
			oidcClients: [],
			error: (err as Error).message
		};
	}
};
