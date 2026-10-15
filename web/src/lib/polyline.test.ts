import { describe, expect, it } from 'vitest';
import { decodePolyline, decodePolylineLonLat } from './polyline';

// Reference vector from Google's polyline algorithm documentation.
const GOOGLE = '_p~iF~ps|U_ulLnnqC_mqNvxq`@';

describe('decodePolyline', () => {
	it('returns [] for empty input', () => {
		expect(decodePolyline('')).toEqual([]);
	});

	it('decodes the reference polyline as [lat, lon]', () => {
		expect(decodePolyline(GOOGLE)).toEqual([
			[38.5, -120.2],
			[40.7, -120.95],
			[43.252, -126.453]
		]);
	});

	it('honours a custom precision', () => {
		// Same integer deltas scaled by 1e6 instead of 1e5.
		const [first] = decodePolyline(GOOGLE, 6);
		expect(first).toEqual([3.85, -12.02]);
	});

	it('decodes a single zero point', () => {
		expect(decodePolyline('??')).toEqual([[0, 0]]);
	});
});

describe('decodePolylineLonLat', () => {
	it('swaps to [lon, lat]', () => {
		expect(decodePolylineLonLat(GOOGLE)[0]).toEqual([-120.2, 38.5]);
		expect(decodePolylineLonLat('')).toEqual([]);
	});
});
