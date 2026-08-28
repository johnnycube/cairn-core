import type { StyleSpecification } from 'maplibre-gl';

// Default basemap for every interactive map. OpenFreeMap serves OpenMapTiles
// vector styles without an API key (CARTO's free raster basemap started
// watermarking tiles with "API KEY REQUIRED" in 2026-08). Operators can point
// VITE_MAP_STYLE_URL at any MapLibre style (MapTiler, keyed CARTO, self-hosted).
const DEFAULT_STYLE_URL = 'https://tiles.openfreemap.org/styles/liberty';
const DEFAULT_ATTRIBUTION =
	'<a href="https://openfreemap.org" target="_blank">OpenFreeMap</a> ' +
	'<a href="https://www.openmaptiles.org/" target="_blank">© OpenMapTiles</a> ' +
	'<a href="https://www.openstreetmap.org/copyright" target="_blank">© OpenStreetMap contributors</a>';

export function basemapStyle(): string | StyleSpecification {
	return (import.meta.env.VITE_MAP_STYLE_URL as string | undefined) || DEFAULT_STYLE_URL;
}

// Attribution for the default style only; an operator-supplied style carries
// its own source attribution.
export function basemapAttribution(): string | undefined {
	return import.meta.env.VITE_MAP_STYLE_URL ? undefined : DEFAULT_ATTRIBUTION;
}
