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
