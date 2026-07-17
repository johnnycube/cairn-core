import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Public page. If already signed in, skip it. The invite code is read from
// ?code= (shareable signup link) on the client.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (user) {
		redirect(302, '/');
	}
	return {};
};
