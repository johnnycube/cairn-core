import { afterEach, describe, expect, it, vi } from 'vitest';

const calls = vi.hoisted(() => [] as unknown[]);
vi.mock('maplibre-gl', () => ({ setWorkerUrl: (u: unknown) => calls.push(u) }));
vi.mock('maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url', () => ({ default: '/w.mjs' }));

import { basemapStyle } from './basemap';

afterEach(() => vi.unstubAllEnvs());

describe('basemap', () => {
	it('pins the maplibre worker to the bundled asset on import', () => {
		expect(calls).toEqual(['/w.mjs']);
	});

	it('defaults to OpenFreeMap', () => {
		vi.stubEnv('VITE_MAP_STYLE_URL', '');
		expect(basemapStyle()).toBe('https://tiles.openfreemap.org/styles/liberty');
	});

	it('honours VITE_MAP_STYLE_URL', () => {
		vi.stubEnv('VITE_MAP_STYLE_URL', 'https://example.test/style.json');
		expect(basemapStyle()).toBe('https://example.test/style.json');
	});
});
