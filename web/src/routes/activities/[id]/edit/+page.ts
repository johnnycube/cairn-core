import { error, redirect } from '@sveltejs/kit';
import { connectClients, isUnauthenticated, Code, ConnectError } from '$lib/connect';
import { ActivityType, Discipline, Privacy } from '$proto/cairn/v1/activity_pb.js';
import { enumName, protoDurationToSeconds } from '$lib/proto-helpers';
import type { PageLoad } from './$types';

// Loads the full activity (incl. tags/privacy) for the edit form, independent
// of the [id] layout's view projection which omits those.
export const load: PageLoad = async ({ fetch, url, params }) => {
	const clients = connectClients(fetch, url.origin);
	try {
		const res = await clients.activity.getActivity({ id: params.id });
		const a = res.activity;
		if (!a) error(404, 'Activity not found');
		return {
			edit: {
				id: a.id,
				title: a.title,
				description: a.description,
				type: enumName(ActivityType, a.type),
				discipline: enumName(Discipline, a.discipline),
				isVirtual: a.isVirtual,
				isEbike: a.isEbike,
				isCommute: a.isCommute,
				isRace: a.isRace,
				customSubtype: a.customSubtype,
				tags: a.tags ?? [],
				privacy: enumName(Privacy, a.privacy) || 'private',
				distanceM: a.summary?.distanceM ?? null,
				elevationGainM: a.summary?.elevationGainM ?? null,
				movingDurationS: protoDurationToSeconds(a.movingDuration)
			}
		};
	} catch (err) {
		if (isUnauthenticated(err)) redirect(302, '/login');
		if (err instanceof ConnectError && err.code === Code.NotFound) error(404, 'Activity not found');
		throw err;
	}
};
