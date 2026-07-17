import type { PageLoad } from './$types';

// Public profile: NO auth guard. Anonymous viewers may see opted-in profiles;
// the API enforces visibility. `user` (may be null) comes from layout data.
export const load: PageLoad = async ({ params, parent }) => {
	const { user } = await parent();
	return { user, username: params.username };
};
