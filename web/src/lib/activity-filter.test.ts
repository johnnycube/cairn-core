import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
	ActivityFilter,
	FLAGS,
	humanize,
	rangeSpecs,
	RANGE_KEYS,
	SORTS
} from './activity-filter.svelte';
import { setPrefs } from './prefs.svelte';

process.env.TZ = 'UTC';

beforeEach(() => setPrefs({ units: 'metric' }));

describe('humanize', () => {
	it('title-cases words', () => {
		expect(humanize('ride_road')).toBe('Ride Road');
	});
});

describe('rangeSpecs', () => {
	it('covers every range key', () => {
		expect(rangeSpecs('metric').map((r) => r.key)).toEqual([...RANGE_KEYS]);
	});

	it('converts display units to SI', () => {
		const si = (units: 'metric' | 'imperial', key: string, v: number) =>
			rangeSpecs(units)
				.find((r) => r.key === key)
				?.toSI(v);
		expect(si('metric', 'distance', 5)).toBe(5000);
		expect(si('imperial', 'distance', 1)).toBeCloseTo(1609.344);
		expect(si('metric', 'duration', 1.5)).toBe(5400);
		expect(si('metric', 'elevation', 10)).toBe(10);
		expect(si('imperial', 'elevation', 3.28084)).toBeCloseTo(1);
		expect(si('metric', 'speed', 36)).toBe(10);
		expect(si('imperial', 'speed', 3600)).toBeCloseTo(1609.344);
		expect(si('metric', 'hr', 150)).toBe(150);
		expect(si('metric', 'power', 250)).toBe(250);
	});

	it('labels units per system', () => {
		const units = (u: 'metric' | 'imperial') => rangeSpecs(u).map((r) => r.unit);
		expect(units('metric')).toEqual(['km', 'h', 'm', 'km/h', 'bpm', 'W']);
		expect(units('imperial')).toEqual(['mi', 'h', 'ft', 'mph', 'bpm', 'W']);
	});
});

describe('ActivityFilter', () => {
	it('defaults to an inactive filter', () => {
		const f = new ActivityFilter();
		expect(f.active).toBe(false);
		expect(f.params().toString()).toBe('sort=date_desc');
	});

	it('seeds from URL params and ignores unknown sorts', () => {
		const f = new ActivityFilter(
			new URLSearchParams(
				'type=run&discipline=run_trail&from=2024-01-01&to=2024-02-01&sort=speed_desc'
			)
		);
		expect(f).toMatchObject({
			type: 'run',
			discipline: 'run_trail',
			from: '2024-01-01',
			to: '2024-02-01',
			sort: 'speed_desc'
		});
		expect(f.active).toBe(true);
		expect(new ActivityFilter(new URLSearchParams('sort=bogus')).sort).toBe(SORTS[0]?.value);
	});

	it('setType toggles and clears the discipline', () => {
		const f = new ActivityFilter();
		f.discipline = 'ride_mtb';
		f.setType('ride');
		expect([f.type, f.discipline]).toEqual(['ride', '']);
		f.setType('ride');
		expect(f.type).toBe('');
	});

	describe('applyPreset', () => {
		beforeEach(() => {
			vi.useFakeTimers();
			vi.setSystemTime(new Date('2024-03-10T12:00:00Z'));
		});
		afterEach(() => vi.useRealTimers());

		it('sets from to today minus N days and toggles off', () => {
			const f = new ActivityFilter();
			f.year = '2023';
			f.applyPreset(7);
			expect([f.preset, f.from, f.to, f.year]).toEqual([7, '2024-03-03', '', '']);
			f.applyPreset(7);
			expect([f.preset, f.from, f.to]).toEqual([null, '', '']);
		});
	});

	it('applyYear spans a half-open year and clears when empty', () => {
		const f = new ActivityFilter();
		f.preset = 30;
		f.year = '2023';
		f.applyYear();
		expect([f.preset, f.from, f.to]).toEqual([null, '2023-01-01', '2024-01-01']);
		f.year = '';
		f.applyYear();
		expect([f.from, f.to]).toEqual(['', '']);
	});

	it('dateEdited drops preset and year', () => {
		const f = new ActivityFilter();
		f.preset = 7;
		f.year = '2023';
		f.dateEdited();
		expect([f.preset, f.year]).toEqual([null, '']);
	});

	it('cycleFlag goes any → true → false → any', () => {
		const f = new ActivityFilter();
		const seen: string[] = [];
		for (let i = 0; i < 3; i++) {
			f.cycleFlag('race');
			seen.push(f.flags.race ?? '');
		}
		expect(seen).toEqual(['true', 'false', '']);
	});

	it('builds query params in SI units', () => {
		const f = new ActivityFilter();
		f.type = 'ride';
		f.discipline = 'ride_road';
		f.from = '2024-01-01';
		f.to = '2024-02-01';
		f.cycleFlag('ebike');
		f.cycleFlag('commute');
		f.cycleFlag('commute');
		f.ranges.distance = { min: '10', max: '' };
		f.ranges.speed = { min: 'abc', max: '36' };
		f.ranges.hr = { min: '-5', max: '0' };
		expect(f.flagsActive).toBe(true);
		expect(f.rangesActive).toBe(true);
		expect(Object.fromEntries(f.params())).toEqual({
			sort: 'date_desc',
			type: 'ride',
			discipline: 'ride_road',
			from: '2024-01-01',
			to: '2024-02-01',
			ebike: 'true',
			commute: 'false',
			distance_min_m: '10000',
			speed_max_mps: '10',
			hr_max_bpm: '0'
		});
	});

	it('uses imperial specs when prefs say so', () => {
		setPrefs({ units: 'imperial' });
		const f = new ActivityFilter();
		f.ranges.elevation = { min: '', max: '3280.84' };
		expect(Number(f.params().get('elevation_max_m'))).toBeCloseTo(1000);
	});

	it('change keys track their inputs', () => {
		const f = new ActivityFilter();
		const imm = f.immediateKey;
		const txt = f.textKey;
		f.from = '2024-01-01';
		expect(f.immediateKey).toBe(imm);
		expect(f.textKey).not.toBe(txt);
		f.sort = 'date_asc';
		expect(f.immediateKey).not.toBe(imm);
	});

	it('clear resets everything but the sort', () => {
		const f = new ActivityFilter(new URLSearchParams('type=run&sort=date_asc'));
		f.year = '2024';
		f.preset = 7;
		for (const fl of FLAGS) f.cycleFlag(fl.key);
		f.ranges.power = { min: '100', max: '' };
		f.clear();
		expect(f.active).toBe(false);
		expect([f.year, f.preset, f.sort]).toEqual(['', null, 'date_asc']);
	});
});
