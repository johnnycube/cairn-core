import { GearKind, type Gear, type GearExternalRef } from '#lib/proto/cairn/v1/gear_pb.js';
import { protoTimestampToISO } from '#lib/proto-helpers.js';

// Gear helpers shared by the gear page, the pickers and the filter bar.
// Mirrors domain.GearKindForActivityType: the picker suggests gear of the
// matching kind; assignment never enforces it.

export type GearKindName = 'bike' | 'shoe' | 'other';

export const GEAR_KINDS: { value: GearKindName; label: string }[] = [
	{ value: 'bike', label: 'Bike' },
	{ value: 'shoe', label: 'Shoes' },
	{ value: 'other', label: 'Other' }
];

export function kindForType(type: string): GearKindName {
	switch (type) {
		case 'ride':
			return 'bike';
		case 'run':
		case 'walk':
		case 'hike':
			return 'shoe';
		default:
			return 'other';
	}
}

export function kindName(k: GearKind): GearKindName {
	switch (k) {
		case GearKind.BIKE:
			return 'bike';
		case GearKind.SHOE:
			return 'shoe';
		default:
			return 'other';
	}
}

export function kindValue(name: string): GearKind {
	switch (name) {
		case 'bike':
			return GearKind.BIKE;
		case 'shoe':
			return GearKind.SHOE;
		default:
			return GearKind.OTHER;
	}
}

// The plain shape pages keep in state (no bigint, no proto classes).
export interface GearView {
	id: string;
	kind: GearKindName;
	name: string;
	brand: string;
	model: string;
	notes: string;
	purchaseDate: string;
	retired: boolean;
	retiredAt: string;
	totalDistanceM: number;
	totalMovingS: number;
	activityCount: number;
	refs: GearRefView[];
}

export interface GearRefView {
	provider: string;
	externalAccountId: string;
	externalId: string;
	providerName: string;
	providerDistanceM: number | null;
	primary: boolean;
	gearId: string;
	kind: GearKindName;
	brand: string;
	model: string;
	description: string;
	retired: boolean;
	lastSeenAt: string;
}

export function gearToView(g: Gear): GearView {
	return {
		id: g.id,
		kind: kindName(g.kind),
		name: g.name,
		brand: g.brand,
		model: g.model,
		notes: g.notes,
		purchaseDate: g.purchaseDate,
		retired: g.retired,
		retiredAt: g.retiredAt ? protoTimestampToISO(g.retiredAt) : '',
		totalDistanceM: g.totalDistanceM,
		totalMovingS: Number(g.totalMovingS),
		activityCount: Number(g.activityCount),
		refs: g.externalRefs.map(refToView)
	};
}

export function refToView(r: GearExternalRef): GearRefView {
	return {
		provider: r.provider,
		externalAccountId: r.externalAccountId,
		externalId: r.externalId,
		providerName: r.providerName,
		providerDistanceM: r.providerDistanceM ?? null,
		primary: r.primary,
		gearId: r.gearId,
		kind: kindName(r.kind),
		brand: r.brand,
		model: r.model,
		description: r.description,
		retired: r.retired,
		lastSeenAt: protoTimestampToISO(r.lastSeenAt)
	};
}

// Display label: "Canyon Grail" falls back to the name alone.
export function gearLabel(g: Pick<GearView, 'name' | 'brand' | 'model'>): string {
	const detail = [g.brand, g.model].filter(Boolean).join(' ');
	return detail && detail !== g.name ? `${g.name} (${detail})` : g.name;
}

// pickerOptions lists gear for an activity of `type`: matching kind first,
// other kinds after a divider, retired gear only when it is the current
// choice (it stays attached, it just is not offered any more).
export function pickerOptions(
	gear: GearView[],
	type: string,
	currentId: string
): { matching: GearView[]; others: GearView[] } {
	const kind = kindForType(type);
	const visible = gear.filter((g) => !g.retired || g.id === currentId);
	return {
		matching: visible.filter((g) => g.kind === kind),
		others: visible.filter((g) => g.kind !== kind)
	};
}
