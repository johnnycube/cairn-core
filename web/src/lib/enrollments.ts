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

// enrollmentRequest builds the POST body, or null while name or provider is missing.
export function enrollmentRequest(
	name: string,
	provider: string,
	expiresDays: number,
	note: string
): EnrollmentRequest | null {
	const n = name.trim();
	if (!n || !provider) return null;
	return {
		name: n,
		provider,
		expires_in_hours: Math.max(1, Math.round(expiresDays * 24)),
		note: note.trim()
	};
}
