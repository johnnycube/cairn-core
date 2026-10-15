import { describe, expect, it } from 'vitest';
import { m } from '$lib/paraglide/messages';
import type { ActivityStreamView } from './+layout';
import { buildSeries } from './streamSeries';

function stream(over: Partial<ActivityStreamView>): ActivityStreamView {
	return {
		sourceID: 's',
		resolutionHz: 1,
		sampleCount: 2,
		coordinates: [],
		track: [],
		offsets: [0, 1],
		hr: null,
		power: null,
		cadence: null,
		speed: null,
		altitude: null,
		...over
	};
}

describe('buildSeries', () => {
	it('returns [] without a stream or channels', () => {
		expect(buildSeries(null)).toEqual([]);
		expect(buildSeries(stream({}))).toEqual([]);
	});

	it('includes every present channel in a fixed order', () => {
		const xs = buildSeries(
			stream({
				hr: [120, null],
				power: [200, 210],
				cadence: [80, 85],
				speed: [5, null],
				altitude: [100, 101]
			})
		);
		expect(xs.map((s) => s.unit)).toEqual(['bpm', 'W', 'rpm', 'km/h', 'm']);
		expect(xs[0]?.label).toBe(m.detail_label_heart_rate());
		expect(xs.at(-1)?.fill).toBe(true);
		expect(xs.slice(0, -1).some((s) => s.fill)).toBe(false);
	});

	it('converts speed to km/h with one decimal and keeps gaps', () => {
		const [speed] = buildSeries(stream({ speed: [2.777, null, 0] }));
		expect(speed?.values).toEqual([10, null, 0]);
	});
});
