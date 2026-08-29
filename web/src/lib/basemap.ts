import { setWorkerUrl, type StyleSpecification } from 'maplibre-gl';
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url';

// MapLibre ≥6 resolves its worker as `./maplibre-gl-worker.mjs` relative to
// import.meta.url at runtime. In the production bundle that is a hashed chunk
// directory where no such file exists, so the worker 404s and the map stays
// blank (no vector tiles, no GeoJSON — both are parsed in the worker). Vite
// dev serves node_modules directly, which is why it only breaks in prod. Pin
// the worker to the Vite-bundled asset instead.
setWorkerUrl(workerUrl);

// Default basemap for every interactive map. OpenFreeMap serves OpenMapTiles
// vector styles without an API key (CARTO's free raster basemap started
// watermarking tiles with "API KEY REQUIRED" in 2026-08). Operators can point
// VITE_MAP_STYLE_URL at any MapLibre style (MapTiler, keyed CARTO, self-hosted).
// Attribution comes from the style's own sources (OpenFreeMap's TileJSON
// carries it), so none is added here.
const DEFAULT_STYLE_URL = 'https://tiles.openfreemap.org/styles/liberty';
export function basemapStyle(): string | StyleSpecification {
	return (import.meta.env.VITE_MAP_STYLE_URL as string | undefined) || DEFAULT_STYLE_URL;
}
