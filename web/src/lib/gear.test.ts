import { describe, expect, it } from 'vitest';
import { gearLabel, kindForType, pickerOptions, type GearView } from './gear';

function g(over: Partial<GearView>): GearView {
	return {
		id: over.id ?? 'x',
		kind: over.kind ?? 'bike',
		name: over.name ?? 'Bike',
		brand: '',
		model: '',
		notes: '',
		purchaseDate: '',
		retired: over.retired ?? false,
		retiredAt: '',
		totalDistanceM: 0,
		totalMovingS: 0,
		activityCount: 0,
		refs: [],
		...over
	};
}

describe('kindForType', () => {
	it('maps rides to bikes, foot sports to shoes, the rest to other', () => {
		expect(kindForType('ride')).toBe('bike');
		expect(kindForType('run')).toBe('shoe');
		expect(kindForType('hike')).toBe('shoe');
		expect(kindForType('swim')).toBe('other');
	});
});

describe('pickerOptions', () => {
	const bike = g({ id: 'b', kind: 'bike', name: 'Canyon' });
	const shoe = g({ id: 's', kind: 'shoe', name: 'Pegasus' });
	const oldBike = g({ id: 'o', kind: 'bike', name: 'Old', retired: true });

	it('puts the matching kind first and hides retired gear', () => {
		const { matching, others } = pickerOptions([shoe, oldBike, bike], 'ride', '');
		expect(matching.map((x) => x.id)).toEqual(['b']);
		expect(others.map((x) => x.id)).toEqual(['s']);
	});

	it('keeps retired gear that is the current choice', () => {
		const { matching } = pickerOptions([oldBike, bike], 'ride', 'o');
		expect(matching.map((x) => x.id)).toEqual(['o', 'b']);
	});
});

describe('gearLabel', () => {
	it('adds brand and model when they say more than the name', () => {
		expect(gearLabel({ name: 'Gravel', brand: 'Canyon', model: 'Grail' })).toBe(
			'Gravel (Canyon Grail)'
		);
		expect(gearLabel({ name: 'Canyon Grail', brand: 'Canyon', model: 'Grail' })).toBe(
			'Canyon Grail'
		);
		expect(gearLabel({ name: 'Pegasus', brand: '', model: '' })).toBe('Pegasus');
	});
});

import { create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import { GearExternalRefSchema, GearKind, GearSchema } from '#lib/proto/cairn/v1/gear_pb.js';
import { ActivityType } from '#lib/proto/cairn/v1/activity_pb.js';
import { gearToView, kindName, kindValue, refToView } from './gear';

describe('kind conversions', () => {
	it('round-trips the three kinds and maps unknown to other', () => {
		for (const k of ['bike', 'shoe', 'other'] as const) {
			expect(kindName(kindValue(k))).toBe(k);
		}
		expect(kindValue('wheel')).toBe(GearKind.OTHER);
		expect(kindName(GearKind.UNSPECIFIED)).toBe('other');
	});
});

describe('gearToView', () => {
	it('maps the wire message, bigint totals and the optional retired_at', () => {
		const ref = create(GearExternalRefSchema, {
			provider: 'strava',
			externalAccountId: 'acct',
			externalId: 'b1',
			providerName: 'Canyon',
			primary: true,
			gearId: 'g1',
			kind: GearKind.BIKE,
			defaultFor: [ActivityType.RIDE],
			lastSeenAt: create(TimestampSchema, { seconds: 1_700_000_000n })
		});
		const g = create(GearSchema, {
			id: 'g1',
			kind: GearKind.BIKE,
			name: 'Gravel',
			brand: 'Canyon',
			model: 'Grail',
			purchaseDate: '2024-03-01',
			retired: true,
			retiredAt: create(TimestampSchema, { seconds: 1_700_000_000n }),
			totalDistanceM: 1500,
			totalMovingS: 3600n,
			activityCount: 2n,
			externalRefs: [ref]
		});
		const v = gearToView(g);
		expect(v).toMatchObject({
			id: 'g1',
			kind: 'bike',
			name: 'Gravel',
			retired: true,
			retiredAt: '2023-11-14T22:13:20.000Z',
			totalMovingS: 3600,
			activityCount: 2
		});
		expect(v.refs[0]).toMatchObject({
			provider: 'strava',
			providerName: 'Canyon',
			providerDistanceM: null,
			primary: true,
			gearId: 'g1',
			kind: 'bike',
			lastSeenAt: '2023-11-14T22:13:20.000Z'
		});
	});

	it('leaves retired_at empty and keeps a provider odometer', () => {
		const v = gearToView(create(GearSchema, { id: 'x', name: 'Shoe', kind: GearKind.SHOE }));
		expect(v.retiredAt).toBe('');
		expect(v.refs).toEqual([]);
		const r = refToView(
			create(GearExternalRefSchema, {
				provider: 'garmin',
				externalId: '7',
				providerDistanceM: 12.5
			})
		);
		expect(r.providerDistanceM).toBe(12.5);
		expect(r.gearId).toBe('');
	});
});
