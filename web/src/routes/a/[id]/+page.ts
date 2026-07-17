import type { PageLoad } from './$types';

// Projected single-activity view for non-owners (followers/public). No auth
// guard — the API enforces visibility. Owners are redirected to the full page.
export const load: PageLoad = async ({ params }) => {
	return { id: params.id };
};
