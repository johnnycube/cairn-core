import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// The feed itself is fetched client-side from /api/activities/feed so filter
// and sort changes re-query without a full navigation. This loader only
// guards auth.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return {};
};
