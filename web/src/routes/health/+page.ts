import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Health metrics. Data fetched client-side from /api/health; auth guard only.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return { user };
};
