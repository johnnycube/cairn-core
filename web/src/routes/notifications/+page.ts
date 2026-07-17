import { redirect } from '@sveltejs/kit';
import { connectClients, isUnauthenticated } from '$lib/connect';
import {
	type NotificationEvent,
	NotificationEventType,
	NotificationSeverity
} from '$proto/cairn/v1/notification_pb.js';
import { enumName, protoTimestampToISO } from '$lib/proto-helpers';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url, parent }) => {
	const { user } = await parent();
	if (!user) {
		redirect(302, '/login');
	}

	const onlyUnread = url.searchParams.get('unread') === '1';
	const clients = connectClients(fetch, url.origin);

	try {
		const res = await clients.notification.listNotifications({
			onlyUnread,
			page: { cursor: '', limit: 50 }
		});
		return {
			onlyUnread,
			notifications: res.notifications.map(notificationToView)
		};
	} catch (err) {
		if (isUnauthenticated(err)) {
			redirect(302, '/login');
		}
		console.warn('ListNotifications failed', err);
		return { onlyUnread, notifications: [] };
	}
};

export interface NotificationView {
	id: string;
	type: string;
	severity: string;
	titleI18nKey: string;
	bodyI18nKey: string;
	params: Record<string, string>;
	activityId?: string;
	segmentId?: string;
	read: boolean;
	createdAt: string;
	coalesceCount: number;
}

function notificationToView(n: NotificationEvent): NotificationView {
	return {
		id: n.id,
		type: enumName(NotificationEventType, n.type) || `type_${n.type}`,
		severity: enumName(NotificationSeverity, n.severity) || 'unspecified',
		titleI18nKey: n.titleI18nKey,
		bodyI18nKey: n.bodyI18nKey,
		params: n.i18nParams ?? {},
		activityId: n.activityId,
		segmentId: n.segmentId,
		read: n.read,
		coalesceCount: n.coalesceCount,
		createdAt: protoTimestampToISO(n.createdAt)
	};
}
