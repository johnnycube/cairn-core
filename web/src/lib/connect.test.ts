import { afterEach, describe, expect, it, vi } from 'vitest';
import { Code, ConnectError, connectClients, isUnauthenticated } from './connect';

afterEach(() => {
	vi.unstubAllGlobals();
	vi.resetModules();
});

// Fake fetch answering every call with a Connect JSON error.
function failingFetch(code: string, urls: string[]): typeof fetch {
	return async (input) => {
		urls.push(input instanceof Request ? input.url : String(input));
		return new Response(JSON.stringify({ code, message: 'nope' }), {
			status: code === 'unauthenticated' ? 401 : 403,
			headers: { 'Content-Type': 'application/json' }
		});
	};
}

describe('isUnauthenticated', () => {
	it('matches only Unauthenticated ConnectErrors', () => {
		expect(isUnauthenticated(new ConnectError('x', Code.Unauthenticated))).toBe(true);
		expect(isUnauthenticated(new ConnectError('x', Code.PermissionDenied))).toBe(false);
		expect(isUnauthenticated(new Error('x'))).toBe(false);
		expect(isUnauthenticated(null)).toBe(false);
	});
});

describe('connectClients', () => {
	it('exposes every service client', () => {
		const c = connectClients(fetch, 'http://h');
		expect(Object.keys(c).sort()).toEqual(
			[
				'activity',
				'admin',
				'auth',
				'externalAccount',
				'gear',
				'metrics',
				'notification',
				'segment'
			].sort()
		);
	});

	it('routes calls through the given fetch and origin and decodes errors', async () => {
		const urls: string[] = [];
		const c = connectClients(failingFetch('unauthenticated', urls), 'http://api.test');
		const err = await c.auth.getLoginMethods({}).catch((e: unknown) => e);
		expect(urls).toEqual(['http://api.test/cairn.v1.AuthService/GetLoginMethods']);
		expect(isUnauthenticated(err)).toBe(true);
	});

	it('defaults the base URL to the window origin', async () => {
		vi.stubGlobal('window', { location: { origin: 'http://win.test' } });
		const mod = await import('./connect');
		const urls: string[] = [];
		const err = await mod
			.connectClients(failingFetch('permission_denied', urls))
			.auth.getLoginMethods({})
			.catch((e: unknown) => e);
		expect(urls[0]).toBe('http://win.test/cairn.v1.AuthService/GetLoginMethods');
		expect(err).toBeInstanceOf(mod.ConnectError);
		expect((err as ConnectError).code).toBe(Code.PermissionDenied);
	});

	it('provides a singleton', async () => {
		const mod = await import('./connect');
		expect(mod.clients.activity).toBeDefined();
	});
});
