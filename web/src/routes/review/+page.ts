import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Confidence-band review queue. Data fetched client-side from /api/review-queue;
// this loader only guards auth.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return { user };
};
