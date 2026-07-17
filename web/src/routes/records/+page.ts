import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Cross-provider personal records. Data is fetched client-side from /api/records;
// this loader only guards auth.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return { user };
};
