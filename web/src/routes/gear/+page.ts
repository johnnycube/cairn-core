import { redirect } from '@sveltejs/kit';
import { connectClients, isUnauthenticated } from '#lib/connect.js';
import { ActivityType } from '#lib/proto/cairn/v1/activity_pb.js';
import { enumName } from '#lib/proto-helpers.js';
import { gearToView, refToView } from '#lib/gear.js';
import type { PageLoad } from './$types';

export type GearDefaultView = { activityType: string; gearId: string };

export const load: PageLoad = async ({ fetch, url, parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}
	const clients = connectClients(fetch, url.origin);
	try {
		const [gear, pending, defaults] = await Promise.all([
			clients.gear.listGear({ includeRetired: true }),
			clients.gear.listPendingGearRefs({}),
			clients.gear.listGearDefaults({})
		]);
		return {
			gear: gear.gear.map(gearToView),
			pending: pending.refs.map(refToView),
			defaults: defaults.defaults.map((d) => ({
				activityType: enumName(ActivityType, d.activityType),
				gearId: d.gearId
			})) as GearDefaultView[]
		};
	} catch (err) {
		if (isUnauthenticated(err)) redirect(302, '/login');
		throw err;
	}
};
