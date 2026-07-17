import type { Duration, Timestamp } from '@bufbuild/protobuf/wkt';

// Tiny helpers that bridge protobuf-es well-known types (bigint-based
// Timestamp/Duration) into the plain JS shapes pages return to the
// browser. SvelteKit's devalue serialiser can handle bigint, but
// strings round-trip faster and the UI never needs nanosecond
// precision for activity timestamps.

export function protoTimestampToISO(ts: Timestamp | undefined): string {
	if (!ts) return '';
	const ms = Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6);
	return new Date(ms).toISOString();
}

export function protoDurationToSeconds(d: Duration | undefined): number {
	if (!d) return 0;
	return Number(d.seconds) + d.nanos / 1e9;
}

// Drop the proto enum prefix and return a stable lowercase identifier.
// E.g. ActivityType[1] → "RIDE" → "ride"; Discipline[10] → "RIDE_ROAD"
// → "ride_road". Used wherever the UI needs the domain-style string
// rather than the proto-enum-int.
export function enumName(enumObj: Record<number, string>, value: number): string {
	const raw = enumObj[value];
	if (!raw) return '';
	// proto-es UNSPECIFIED maps to value 0 — treat as empty for UX.
	if (raw === 'UNSPECIFIED') return '';
	return raw.toLowerCase();
}

// Reverse of enumName: map a lowercase identifier ("ride", "ride_road") back to
// its proto enum int via the numeric enum's reverse mapping. Empty/unknown →
// 0 (UNSPECIFIED). E.g. enumValue(ActivityType, "ride") → 1.
export function enumValue(enumObj: Record<string, number | string>, name: string): number {
	if (!name) return 0;
	const v = (enumObj as Record<string, number>)[name.toUpperCase()];
	return typeof v === 'number' ? v : 0;
}
