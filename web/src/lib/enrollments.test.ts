import { describe, expect, it } from 'vitest';
import { enrollmentRequest, enrollmentStatus } from './enrollments';

describe('enrollmentStatus', () => {
	const now = Date.parse('2026-10-15T12:00:00Z');
	const future = '2026-10-16T12:00:00Z';
	const past = '2026-10-14T12:00:00Z';

	it('is active before expiry', () => {
		expect(enrollmentStatus({ expires_at: future }, now)).toBe('active');
		expect(enrollmentStatus({}, now)).toBe('active');
	});

	it('is expired at or after expiry', () => {
		expect(enrollmentStatus({ expires_at: past }, now)).toBe('expired');
		expect(enrollmentStatus({ expires_at: '2026-10-15T12:00:00Z' }, now)).toBe('expired');
	});

	it('flags enrollments without provider for re-enrollment, even when expired', () => {
		expect(enrollmentStatus({ reenroll_required: true, expires_at: future }, now)).toBe('reenroll');
		expect(enrollmentStatus({ reenroll_required: true, expires_at: past }, now)).toBe('reenroll');
	});

	it('reports revocation first', () => {
		expect(enrollmentStatus({ is_revoked: true, reenroll_required: true }, now)).toBe('revoked');
	});
});

describe('enrollmentRequest', () => {
	it('requires name and provider', () => {
		expect(enrollmentRequest('', 'strava', 30, '')).toBeNull();
		expect(enrollmentRequest('  ', 'strava', 30, '')).toBeNull();
		expect(enrollmentRequest('w1', '', 30, '')).toBeNull();
	});

	it('builds the body with trimmed fields and hours', () => {
		expect(enrollmentRequest(' w1 ', 'garmin', 30, ' box-7 ')).toEqual({
			name: 'w1',
			provider: 'garmin',
			expires_in_hours: 720,
			note: 'box-7'
		});
	});

	it('clamps the expiry to at least one hour', () => {
		expect(enrollmentRequest('w1', 'strava', 0, '')?.expires_in_hours).toBe(1);
	});
});
