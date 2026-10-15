import { describe, expect, it } from 'vitest';
import { ACTIVITY_TYPES, DISCIPLINES, typeLabel } from './activity-types';

describe('activity types', () => {
	it('has unique types', () => {
		expect(new Set(ACTIVITY_TYPES).size).toBe(ACTIVITY_TYPES.length);
	});

	it('only defines disciplines for known types, prefixed by the type', () => {
		for (const [type, subs] of Object.entries(DISCIPLINES)) {
			expect(ACTIVITY_TYPES).toContain(type);
			for (const d of subs) expect(d.startsWith(`${type}_`)).toBe(true);
		}
	});

	it('typeLabel replaces every underscore', () => {
		expect(typeLabel('ski_back_country')).toBe('ski back country');
		expect(typeLabel('ride')).toBe('ride');
	});
});
