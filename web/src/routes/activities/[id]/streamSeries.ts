import { m } from '$lib/paraglide/messages';
import type { ActivityStreamView } from './+layout';

export interface ChartSeries {
	label: string;
	values: (number | null)[];
	color: string;
	unit?: string;
	fill?: boolean;
}

// Build the StreamChart series for an activity — only includes channels that
// actually carry data. Shared by the activity detail page and the full-screen
// streams subpage so both render identical series.
export function buildSeries(stream: ActivityStreamView | null): ChartSeries[] {
	if (!stream) return [];
	const xs: ChartSeries[] = [];
	if (stream.hr) {
		xs.push({ label: m.detail_label_heart_rate(), values: stream.hr, color: '#ef4444', unit: 'bpm' });
	}
	if (stream.power) {
		xs.push({ label: 'Power', values: stream.power, color: '#ec7a45', unit: 'W' });
	}
	if (stream.cadence) {
		xs.push({ label: 'Cadence', values: stream.cadence, color: '#10b981', unit: 'rpm' });
	}
	if (stream.speed) {
		// m/s → km/h for a readable axis.
		xs.push({
			label: 'Speed',
			values: stream.speed.map((v) => (v == null ? null : Math.round(v * 3.6 * 10) / 10)),
			color: '#38bdf8',
			unit: 'km/h'
		});
	}
	if (stream.altitude) {
		xs.push({ label: 'Elevation', values: stream.altitude, color: '#a78bfa', unit: 'm', fill: true });
	}
	return xs;
}
