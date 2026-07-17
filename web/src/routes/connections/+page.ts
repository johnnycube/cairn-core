import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent }) => {
	const { user, permissions } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	return { user, isAdmin: permissions?.includes('admin') ?? false };
};
