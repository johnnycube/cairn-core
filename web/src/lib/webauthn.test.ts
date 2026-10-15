import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { loginWithPasskey, registerPasskey, supported } from './webauthn';

const b64url = (bytes: number[]) =>
	Buffer.from(bytes).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const buf = (bytes: number[]) => new Uint8Array(bytes).buffer;
const bytesOf = (b: BufferSource | undefined) =>
	b instanceof ArrayBuffer ? [...new Uint8Array(b)] : undefined;

// Bytes exercising '+', '/' and padding in base64.
const CHALLENGE = [0xfb, 0xff, 0xbf, 0x00];
const USER_ID = [1, 2, 3, 4, 5];
const CRED_ID = [0xff, 0xfe];

type Call = { url: string; init: RequestInit | undefined };
let calls: Call[];

function stubFetch(...responses: Response[]) {
	calls = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (url: string, init?: RequestInit) => {
			calls.push({ url, init });
			const r = responses.shift();
			if (!r) throw new Error('unexpected fetch');
			return r;
		})
	);
}

function stubCredentials(creds: Partial<CredentialsContainer>) {
	vi.stubGlobal('navigator', { credentials: creds });
}

const json = (v: unknown) => new Response(JSON.stringify(v), { status: 200 });
const fail = (status: number, body = '') => new Response(body, { status });
const sentBody = (i: number) => JSON.parse(String(calls[i]?.init?.body));

beforeEach(() => {
	calls = [];
});
afterEach(() => vi.unstubAllGlobals());

describe('supported', () => {
	it('is false without a window', () => {
		expect(supported()).toBe(false);
	});

	it('requires PublicKeyCredential and credentials.create', () => {
		vi.stubGlobal('window', {});
		stubCredentials({ create: vi.fn() });
		expect(supported()).toBe(false);
		vi.stubGlobal('window', { PublicKeyCredential: class {} });
		expect(supported()).toBe(true);
		stubCredentials({});
		expect(supported()).toBe(false);
	});
});

describe('registerPasskey', () => {
	it('decodes options, encodes the attestation and posts it', async () => {
		stubFetch(
			json({
				publicKey: {
					challenge: b64url(CHALLENGE),
					rp: { name: 'cairn' },
					user: { id: b64url(USER_ID), name: 'a', displayName: 'A' },
					pubKeyCredParams: [],
					excludeCredentials: [{ type: 'public-key', id: b64url(CRED_ID) }]
				}
			}),
			new Response(null, { status: 200 })
		);
		const create = vi.fn(async (opts?: CredentialCreationOptions) => {
			const pk = opts?.publicKey;
			expect(bytesOf(pk?.challenge)).toEqual(CHALLENGE);
			expect(bytesOf(pk?.user.id)).toEqual(USER_ID);
			expect(bytesOf(pk?.excludeCredentials?.[0]?.id)).toEqual(CRED_ID);
			expect(pk?.rp).toEqual({ name: 'cairn' });
			return {
				id: 'cred',
				rawId: buf(CRED_ID),
				type: 'public-key',
				response: {
					attestationObject: buf([1]),
					clientDataJSON: buf([2, 3]),
					getTransports: () => ['usb']
				},
				getClientExtensionResults: () => ({})
			} as unknown as Credential;
		});
		stubCredentials({ create });

		await registerPasskey('my key/1');

		expect(create).toHaveBeenCalledOnce();
		expect(calls.map((c) => c.url)).toEqual([
			'/auth/webauthn/register/start',
			'/auth/webauthn/register/finish?name=my%20key%2F1'
		]);
		expect(sentBody(1)).toEqual({
			id: 'cred',
			rawId: '__4',
			type: 'public-key',
			response: { attestationObject: 'AQ', clientDataJSON: 'AgM', transports: ['usb'] },
			clientExtensionResults: {}
		});
	});

	it('defaults missing excludeCredentials and transports', async () => {
		stubFetch(
			json({ publicKey: { challenge: 'AA', user: { id: 'AA' } } }),
			new Response(null, { status: 200 })
		);
		stubCredentials({
			create: async (opts?: CredentialCreationOptions) => {
				expect(opts?.publicKey?.excludeCredentials).toEqual([]);
				return {
					id: 'c',
					rawId: buf([]),
					type: 'public-key',
					response: { attestationObject: buf([]), clientDataJSON: buf([]) },
					getClientExtensionResults: () => ({})
				} as unknown as Credential;
			}
		});
		await registerPasskey('k');
		expect(sentBody(1).response.transports).toEqual([]);
	});

	it('surfaces server and cancellation errors', async () => {
		stubFetch(fail(500, '  boom \n'));
		await expect(registerPasskey('k')).rejects.toThrow('boom');

		stubFetch(fail(500));
		await expect(registerPasskey('k')).rejects.toThrow('could not start registration');

		stubFetch(json({ publicKey: { challenge: 'AA', user: { id: 'AA' } } }));
		stubCredentials({ create: async () => null });
		await expect(registerPasskey('k')).rejects.toThrow('registration was cancelled');

		stubFetch(json({ publicKey: { challenge: 'AA', user: { id: 'AA' } } }), fail(400));
		stubCredentials({
			create: async () =>
				({
					id: 'c',
					rawId: buf([]),
					type: 'public-key',
					response: { attestationObject: buf([]), clientDataJSON: buf([]) },
					getClientExtensionResults: () => ({})
				}) as unknown as Credential
		});
		await expect(registerPasskey('k')).rejects.toThrow('registration failed');
	});
});

describe('loginWithPasskey', () => {
	const assertion = (userHandle: ArrayBuffer | null) =>
		({
			id: 'cred',
			rawId: buf(CRED_ID),
			type: 'public-key',
			response: {
				authenticatorData: buf([9]),
				clientDataJSON: buf([8]),
				signature: buf([7, 6]),
				userHandle
			},
			getClientExtensionResults: () => ({ appid: false })
		}) as unknown as Credential;

	it('decodes options, encodes the assertion and posts it', async () => {
		stubFetch(
			json({
				publicKey: {
					challenge: b64url(CHALLENGE),
					allowCredentials: [{ type: 'public-key', id: b64url(CRED_ID) }]
				}
			}),
			new Response(null, { status: 200 })
		);
		stubCredentials({
			get: async (opts?: CredentialRequestOptions) => {
				expect(bytesOf(opts?.publicKey?.challenge)).toEqual(CHALLENGE);
				expect(bytesOf(opts?.publicKey?.allowCredentials?.[0]?.id)).toEqual(CRED_ID);
				return assertion(buf(USER_ID));
			}
		});

		await loginWithPasskey();

		expect(calls[1]?.url).toBe('/auth/webauthn/login/finish');
		expect(sentBody(1)).toEqual({
			id: 'cred',
			rawId: '__4',
			type: 'public-key',
			response: {
				authenticatorData: 'CQ',
				clientDataJSON: 'CA',
				signature: 'BwY',
				userHandle: b64url(USER_ID)
			},
			clientExtensionResults: { appid: false }
		});
	});

	it('sends a null userHandle and defaults allowCredentials', async () => {
		stubFetch(json({ publicKey: { challenge: 'AA' } }), new Response(null, { status: 200 }));
		stubCredentials({
			get: async (opts?: CredentialRequestOptions) => {
				expect(opts?.publicKey?.allowCredentials).toEqual([]);
				return assertion(null);
			}
		});
		await loginWithPasskey();
		expect(sentBody(1).response.userHandle).toBeNull();
	});

	it('surfaces server and cancellation errors', async () => {
		stubFetch(fail(500));
		await expect(loginWithPasskey()).rejects.toThrow('could not start login');

		stubFetch(json({ publicKey: { challenge: 'AA' } }));
		stubCredentials({ get: async () => null });
		await expect(loginWithPasskey()).rejects.toThrow('login was cancelled');

		stubFetch(json({ publicKey: { challenge: 'AA' } }), fail(401, 'bad sig'));
		stubCredentials({ get: async () => assertion(null) });
		await expect(loginWithPasskey()).rejects.toThrow('bad sig');

		stubFetch(json({ publicKey: { challenge: 'AA' } }), fail(401));
		await expect(loginWithPasskey()).rejects.toThrow('login failed');
	});
});
