import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const locale = vi.hoisted(() => ({ current: 'de' }));
vi.mock('#lib/paraglide/runtime.js', () => ({ getLocale: () => locale.current }));

import {
	formatDate,
	formatDateOnly,
	formatDistance,
	formatDuration,
	formatElevation,
	formatHeartRate,
	formatPace,
	formatPower,
	formatRelativeDate,
	formatSpeed,
	formatTemp
} from './format';
import { setPrefs } from './prefs.svelte';

// ICU versions differ on the space before AM/PM.
const norm = (s: string) => s.replace(/[\u00a0\u202f]/g, ' ');

// Dates are rendered in local time; pin it.
process.env.TZ = 'UTC';

beforeEach(() => {
	locale.current = 'de';
	setPrefs({ dateFormat: '', timeFormat: '', units: 'metric' });
});

describe('null handling', () => {
	it('renders a dash for missing values', () => {
		for (const f of [
			formatDistance,
			formatDuration,
			formatPace,
			formatElevation,
			formatSpeed,
			formatTemp,
			formatHeartRate,
			formatPower
		]) {
			expect(f(null)).toBe('—');
			expect(f(undefined)).toBe('—');
		}
	});
});

describe('formatDistance', () => {
	it('uses metres below 1 km and rounds', () => {
		expect(formatDistance(999.4)).toBe('999 m');
	});

	it('uses km with locale decimals', () => {
		expect(formatDistance(12345)).toBe('12,35 km');
		locale.current = 'en';
		expect(formatDistance(12345)).toBe('12.35 km');
	});

	it('falls back to de-DE for unknown locales', () => {
		locale.current = 'fr';
		expect(formatDistance(1500)).toBe('1,50 km');
	});

	it('uses feet below a tenth of a mile, miles above', () => {
		setPrefs({ units: 'imperial' });
		expect(formatDistance(100)).toBe('328 ft');
		locale.current = 'en';
		expect(formatDistance(1609.344 * 2.5)).toBe('2.50 mi');
	});
});

describe('formatDuration', () => {
	it('formats m:ss under an hour', () => {
		expect(formatDuration(0)).toBe('0:00');
		expect(formatDuration(65)).toBe('1:05');
	});

	it('formats h:mm:ss and rounds seconds', () => {
		expect(formatDuration(3599.6)).toBe('1:00:00');
		expect(formatDuration(3723)).toBe('1:02:03');
	});
});

describe('formatPace', () => {
	it('rejects non-positive speeds', () => {
		expect(formatPace(0)).toBe('—');
		expect(formatPace(-1)).toBe('—');
	});

	it('formats min/km', () => {
		expect(formatPace(1000 / 300)).toBe('5:00 /km');
		expect(formatPace(1000 / 330)).toBe('5:30 /km');
	});

	it('formats min/mi', () => {
		setPrefs({ units: 'imperial' });
		expect(formatPace(1609.344 / 480)).toBe('8:00 /mi');
	});
});

describe('formatElevation / formatSpeed / formatTemp', () => {
	it('metric', () => {
		expect(formatElevation(123.6)).toBe('124 m');
		expect(formatSpeed(10)).toBe('36,00 km/h');
		expect(formatSpeed(0)).toBe('0,00 km/h');
		expect(formatSpeed(-1)).toBe('—');
		expect(formatTemp(21.5)).toBe('22°C');
	});

	it('imperial', () => {
		setPrefs({ units: 'imperial' });
		locale.current = 'en';
		expect(formatElevation(100)).toBe('328 ft');
		expect(formatSpeed(1609.344 / 3600)).toBe('1.00 mph');
		expect(formatTemp(100)).toBe('212°F');
	});

	it('heart rate and power pass through', () => {
		expect(formatHeartRate(142)).toBe('142 bpm');
		expect(formatPower(250)).toBe('250 W');
	});
});

describe('date formatting', () => {
	const iso = '2024-03-05T14:07:00Z';

	it('explicit date formats', () => {
		setPrefs({ dateFormat: 'iso' });
		expect(formatDateOnly(iso)).toBe('2024-03-05');
		setPrefs({ dateFormat: 'us' });
		expect(formatDateOnly(iso)).toBe('3/5/2024');
		setPrefs({ dateFormat: 'eu' });
		expect(formatDateOnly(iso)).toBe('5.3.2024');
	});

	it('locale default date format', () => {
		expect(formatDateOnly(iso)).toBe('05.03.2024');
		locale.current = 'en';
		expect(formatDateOnly(iso)).toBe('Mar 5, 2024');
	});

	it('time formats', () => {
		setPrefs({ dateFormat: 'iso', timeFormat: '24h' });
		expect(formatDate(iso)).toBe('2024-03-05 14:07');
		locale.current = 'en';
		setPrefs({ timeFormat: '12h' });
		expect(norm(formatDate(iso))).toBe('2024-03-05 2:07 PM');
		setPrefs({ timeFormat: '' });
		expect(norm(formatDate(iso))).toBe('2024-03-05 2:07 PM');
		locale.current = 'de';
		expect(formatDate(iso)).toBe('2024-03-05 14:07');
	});
});

describe('formatRelativeDate', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2024-03-10T12:00:00Z'));
		locale.current = 'en';
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('picks the largest fitting unit', () => {
		expect(formatRelativeDate('2024-03-10T11:59:30Z')).toBe('30 seconds ago');
		expect(formatRelativeDate('2024-03-10T11:55:00Z')).toBe('5 minutes ago');
		expect(formatRelativeDate('2024-03-10T09:00:00Z')).toBe('3 hours ago');
		expect(formatRelativeDate('2024-03-09T12:00:00Z')).toBe('yesterday');
		expect(formatRelativeDate('2024-03-07T12:00:00Z')).toBe('3 days ago');
	});

	it('falls back to an absolute date after a week', () => {
		setPrefs({ dateFormat: 'iso' });
		expect(formatRelativeDate('2024-03-01T12:00:00Z')).toBe('2024-03-01');
	});

	it('uses now for zero seconds', () => {
		expect(formatRelativeDate('2024-03-10T12:00:00Z')).toBe('now');
	});
});
