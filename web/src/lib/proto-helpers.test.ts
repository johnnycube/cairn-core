import { create } from '@bufbuild/protobuf';
import { DurationSchema, TimestampSchema } from '@bufbuild/protobuf/wkt';
import { describe, expect, it } from 'vitest';
import { enumName, enumValue, protoDurationToSeconds, protoTimestampToISO } from './proto-helpers';

enum Kind {
	UNSPECIFIED = 0,
	RIDE = 1,
	RIDE_ROAD = 10
}

describe('protoTimestampToISO', () => {
	it('returns empty for undefined', () => {
		expect(protoTimestampToISO(undefined)).toBe('');
	});

	it('truncates nanos to milliseconds', () => {
		const ts = create(TimestampSchema, { seconds: 1_700_000_000n, nanos: 123_999_999 });
		expect(protoTimestampToISO(ts)).toBe('2023-11-14T22:13:20.123Z');
	});
});

describe('protoDurationToSeconds', () => {
	it('returns 0 for undefined', () => {
		expect(protoDurationToSeconds(undefined)).toBe(0);
	});

	it('adds fractional nanos', () => {
		const d = create(DurationSchema, { seconds: 90n, nanos: 500_000_000 });
		expect(protoDurationToSeconds(d)).toBe(90.5);
	});
});

describe('enumName', () => {
	it('lowercases the enum key', () => {
		expect(enumName(Kind, Kind.RIDE_ROAD)).toBe('ride_road');
	});

	it('maps UNSPECIFIED and unknown values to empty', () => {
		expect(enumName(Kind, Kind.UNSPECIFIED)).toBe('');
		expect(enumName(Kind, 99)).toBe('');
	});
});

describe('enumValue', () => {
	it('round-trips with enumName', () => {
		expect(enumValue(Kind, enumName(Kind, Kind.RIDE))).toBe(Kind.RIDE);
		expect(enumValue(Kind, 'ride_road')).toBe(10);
	});

	it('returns 0 for empty or unknown names', () => {
		expect(enumValue(Kind, '')).toBe(0);
		expect(enumValue(Kind, 'swim')).toBe(0);
	});
});
