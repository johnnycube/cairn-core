import type { PageLoad } from './$types';

// Shared activity: NO auth guard — an unguessable token grants read-only access.
export const load: PageLoad = async ({ params }) => {
	return { token: params.token };
};
