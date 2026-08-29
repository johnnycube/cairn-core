import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Tracks are fetched client-side from /api/activities/heatmap so filter
// changes re-query without a navigation. This loader only guards auth.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return {};
};
