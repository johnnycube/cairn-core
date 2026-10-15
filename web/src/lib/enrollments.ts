// Worker-enrollment helpers for the admin onboarding UI.

export type EnrollmentState = {
	is_revoked?: boolean;
	expires_at?: string;
	reenroll_required?: boolean;
};

export type EnrollmentStatus = 'revoked' | 'reenroll' | 'expired' | 'active';

// enrollmentStatus picks the badge; an enrollment without provider is refused at connect.
export function enrollmentStatus(e: EnrollmentState, nowMs: number): EnrollmentStatus {
	if (e.is_revoked) return 'revoked';
	if (e.reenroll_required) return 'reenroll';
	if (e.expires_at && new Date(e.expires_at).getTime() <= nowMs) return 'expired';
	return 'active';
}

export type EnrollmentRequest = {
	name: string;
	provider: string;
	expires_in_hours: number;
	note: string;
};

// Same rule as the server (domain.ValidProviderID); keeps NATS wildcards out of subjects.
const providerPattern = /^[a-z0-9_-]{1,48}$/;

export const providerFormatHint =
	'Provider: 1–48 lowercase letters, digits, hyphens or underscores (e.g. polar).';

// providerError explains why a typed provider is rejected, or null when it is valid or still empty.
export function providerError(provider: string): string | null {
	const p = provider.trim();
	if (!p || providerPattern.test(p)) return null;
	return providerFormatHint;
}

// enrollmentRequest builds the POST body, or null while name or provider is missing or malformed.
export function enrollmentRequest(
	name: string,
	provider: string,
	expiresDays: number,
	note: string
): EnrollmentRequest | null {
	const n = name.trim();
	const p = provider.trim();
	if (!n || !providerPattern.test(p)) return null;
	return {
		name: n,
		provider: p,
		expires_in_hours: Math.max(1, Math.round(expiresDays * 24)),
		note: note.trim()
	};
}
