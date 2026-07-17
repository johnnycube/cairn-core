import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent, params }) => {
	const { user } = await parent();
	if (!user) redirect(302, '/login');
	return {
		user,
		type: params.type,
		metric: params.metric,
		window: params.window,
		value: Number(params.value)
	};
};
