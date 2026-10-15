import { describe, expect, it } from 'vitest';
import {
	enrollmentRequest,
	enrollmentStatus,
	providerError,
	providerFormatHint
} from './enrollments';

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

	it('accepts providers outside the built-in list', () => {
		expect(enrollmentRequest('w1', ' polar ', 30, '')?.provider).toBe('polar');
		expect(enrollmentRequest('w1', 'fit_upload-2', 30, '')?.provider).toBe('fit_upload-2');
	});

	it('rejects malformed providers', () => {
		for (const p of ['polar.>', '*', '>', 'po.lar', 'Polar', 'a b', 'p'.repeat(49)]) {
			expect(enrollmentRequest('w1', p, 30, '')).toBeNull();
		}
		expect(enrollmentRequest('w1', 'p'.repeat(48), 30, '')).not.toBeNull();
	});

	it('clamps the expiry to at least one hour', () => {
		expect(enrollmentRequest('w1', 'strava', 0, '')?.expires_in_hours).toBe(1);
	});
});

describe('providerError', () => {
	it('is silent while empty or valid', () => {
		expect(providerError('')).toBeNull();
		expect(providerError('  ')).toBeNull();
		expect(providerError('polar')).toBeNull();
		expect(providerError('strava')).toBeNull();
	});

	it('explains the format for malformed providers', () => {
		for (const p of ['polar.>', '*', 'po.lar', 'Polar', 'p'.repeat(49)]) {
			expect(providerError(p)).toBe(providerFormatHint);
		}
	});
});
