import type { PageLoad } from './$types';

// The dashboard data comes from /api/overview, fetched client-side. This loader
// just threads the user through from the layout.
export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	return { user };
};
