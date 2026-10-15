import { beforeEach, describe, expect, it } from 'vitest';
import { prefs, setPrefs } from './prefs.svelte';

describe('setPrefs', () => {
	beforeEach(() => {
		setPrefs({ dateFormat: '', timeFormat: '', units: 'metric' });
	});

	it('starts at defaults', () => {
		expect({ ...prefs }).toEqual({ dateFormat: '', timeFormat: '', units: 'metric' });
	});

	it('applies provided fields only', () => {
		setPrefs({ dateFormat: 'iso' });
		expect(prefs.dateFormat).toBe('iso');
		expect(prefs.timeFormat).toBe('');
		setPrefs({ timeFormat: '24h', units: 'imperial' });
		expect(prefs).toMatchObject({ dateFormat: 'iso', timeFormat: '24h', units: 'imperial' });
	});

	it('falls back to metric for unknown units', () => {
		setPrefs({ units: 'imperial' });
		setPrefs({ units: 'furlongs' });
		expect(prefs.units).toBe('metric');
	});

	it('leaves fields unchanged when undefined', () => {
		setPrefs({ dateFormat: 'us', units: 'imperial' });
		setPrefs({});
		expect(prefs).toMatchObject({ dateFormat: 'us', units: 'imperial' });
	});
});
