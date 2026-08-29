import { prefs, type Units } from '$lib/prefs.svelte';

// The standard activity filter: one reactive model shared by every view that
// takes the server's activity filter params (/activities feed, /heatmap).
// ActivityFilterBar renders it; pages call params() to build the query.

export type Facet = { value: string; count: number };
export type ActivityFacets = { types: Facet[]; disciplines: Facet[]; years?: Facet[] };

export const SORTS = [
	{ value: 'date_desc', label: 'Newest first' },
	{ value: 'date_asc', label: 'Oldest first' },
	{ value: 'distance_desc', label: 'Longest distance' },
	{ value: 'duration_desc', label: 'Longest duration' },
	{ value: 'elevation_desc', label: 'Most climbing' },
	{ value: 'speed_desc', label: 'Fastest average' }
];

// Quick date presets — "last N days" sets from = today-N, to = open.
export const DATE_PRESETS = [
	{ label: '7d', days: 7 },
	{ label: '30d', days: 30 },
	{ label: '90d', days: 90 },
	{ label: '1y', days: 365 }
];

// Tri-state classification flags: '' = any, 'true', 'false'.
export const FLAGS = [
	{ key: 'virtual', label: 'Virtual' },
	{ key: 'ebike', label: 'E-bike' },
	{ key: 'commute', label: 'Commute' },
	{ key: 'race', label: 'Race' }
] as const;

export const RANGE_KEYS = ['distance', 'duration', 'elevation', 'speed', 'hr', 'power'] as const;
export type RangeKey = (typeof RANGE_KEYS)[number];
export type RangeBounds = { min: string; max: string };
export type RangeSpec = {
	key: RangeKey;
	label: string;
	unit: string;
	toSI: (v: number) => number;
	param: string; // '{}' is replaced with min/max
};

// Numeric ranges are entered in the user's display units (same constants as
// $lib/format) and converted to the SI query params the API expects.
const M_PER_MILE = 1609.344;
const FT_PER_M = 3.28084;
export function rangeSpecs(units: Units): RangeSpec[] {
	const imperial = units === 'imperial';
	return [
		{
			key: 'distance',
			label: 'Distance',
			unit: imperial ? 'mi' : 'km',
			toSI: (v) => (imperial ? v * M_PER_MILE : v * 1000),
			param: 'distance_{}_m'
		},
		{ key: 'duration', label: 'Duration', unit: 'h', toSI: (v) => v * 3600, param: 'duration_{}_s' },
		{
			key: 'elevation',
			label: 'Elevation gain',
			unit: imperial ? 'ft' : 'm',
			toSI: (v) => (imperial ? v / FT_PER_M : v),
			param: 'elevation_{}_m'
		},
		{
			key: 'speed',
			label: 'Avg speed',
			unit: imperial ? 'mph' : 'km/h',
			toSI: (v) => (imperial ? (v * M_PER_MILE) / 3600 : v / 3.6),
			param: 'speed_{}_mps'
		},
		{ key: 'hr', label: 'Avg heart rate', unit: 'bpm', toSI: (v) => v, param: 'hr_{}_bpm' },
		{ key: 'power', label: 'Avg power', unit: 'W', toSI: (v) => v, param: 'power_{}_w' }
	];
}

function emptyRanges(): Record<RangeKey, RangeBounds> {
	return Object.fromEntries(RANGE_KEYS.map((k) => [k, { min: '', max: '' }])) as Record<
		RangeKey,
		RangeBounds
	>;
}
function emptyFlags(): Record<string, string> {
	return { virtual: '', ebike: '', commute: '', race: '' };
}

export function humanize(v: string): string {
	return v.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

export class ActivityFilter {
	type = $state('');
	discipline = $state('');
	sort = $state('date_desc');
	// Half-open date range: from inclusive, to exclusive. Seeded from the URL
	// so dashboard blocks can deep-link a pre-filtered view.
	from = $state('');
	to = $state('');
	preset = $state<number | null>(null);
	// Year is sugar over from/to; options come from the (unfiltered) years facet.
	year = $state('');
	flags = $state<Record<string, string>>(emptyFlags());
	ranges = $state<Record<RangeKey, RangeBounds>>(emptyRanges());
	moreFilters = $state(false);

	constructor(params?: URLSearchParams) {
		if (!params) return;
		this.type = params.get('type') ?? '';
		this.discipline = params.get('discipline') ?? '';
		this.from = params.get('from') ?? '';
		this.to = params.get('to') ?? '';
		const sort = params.get('sort') ?? '';
		if (SORTS.some((s) => s.value === sort)) this.sort = sort;
	}

	readonly rangeSpecs = $derived(rangeSpecs(prefs.units));
	readonly rangesActive = $derived(
		Object.values(this.ranges).some((r) => r.min !== '' || r.max !== '')
	);
	readonly flagsActive = $derived(Object.values(this.flags).some((v) => v !== ''));
	readonly active = $derived(
		this.type !== '' ||
			this.discipline !== '' ||
			this.from !== '' ||
			this.to !== '' ||
			this.flagsActive ||
			this.rangesActive
	);

	// Change keys for pages to react to: chip/select edits should re-query at
	// once, free-text (dates, ranges) after a debounce.
	readonly immediateKey = $derived(
		JSON.stringify([this.type, this.discipline, this.sort, this.flags])
	);
	readonly textKey = $derived(JSON.stringify([this.from, this.to, this.ranges]));

	setType(v: string) {
		this.type = this.type === v ? '' : v;
		this.discipline = '';
	}

	applyPreset(days: number) {
		this.year = '';
		if (this.preset === days) {
			this.preset = null;
			this.from = '';
			this.to = '';
			return;
		}
		this.preset = days;
		const d = new Date();
		d.setDate(d.getDate() - days);
		this.from = d.toISOString().slice(0, 10);
		this.to = '';
	}

	applyYear() {
		this.preset = null;
		if (this.year) {
			this.from = `${this.year}-01-01`;
			this.to = `${Number(this.year) + 1}-01-01`;
		} else {
			this.from = '';
			this.to = '';
		}
	}

	// Manual date edits drop the preset/year shortcuts that no longer apply.
	dateEdited() {
		this.preset = null;
		this.year = '';
	}

	cycleFlag(key: string) {
		this.flags[key] = this.flags[key] === '' ? 'true' : this.flags[key] === 'true' ? 'false' : '';
	}

	clear() {
		this.type = '';
		this.discipline = '';
		this.from = '';
		this.to = '';
		this.preset = null;
		this.year = '';
		this.flags = emptyFlags();
		this.ranges = emptyRanges();
	}

	// Query params in the server's shared filter vocabulary (activities_feed.go).
	params(): URLSearchParams {
		const q = new URLSearchParams({ sort: this.sort });
		if (this.type) q.set('type', this.type);
		if (this.discipline) q.set('discipline', this.discipline);
		if (this.from) q.set('from', this.from);
		if (this.to) q.set('to', this.to);
		for (const f of FLAGS) {
			if (this.flags[f.key]) q.set(f.key, this.flags[f.key]);
		}
		for (const r of this.rangeSpecs) {
			for (const bound of ['min', 'max'] as const) {
				const raw = this.ranges[r.key][bound];
				const v = raw === '' ? NaN : Number(raw);
				if (!Number.isNaN(v) && v >= 0) {
					q.set(r.param.replace('{}', bound), String(r.toSI(v)));
				}
			}
		}
		return q;
	}
}
