import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Data is fetched client-side from /api/segments/{id}. This loader guards auth.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return { user };
};
