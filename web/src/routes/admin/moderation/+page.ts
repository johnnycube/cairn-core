import { error, redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent }) => {
	const { user, permissions } = await parent();
	if (!user) redirect(302, '/login');
	if (!permissions.includes('moderate')) error(403, 'Moderators only');
	return { user };
};
