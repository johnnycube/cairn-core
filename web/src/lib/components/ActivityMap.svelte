<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import maplibregl, { type Map as MapLibreMap } from 'maplibre-gl';
	import 'maplibre-gl/dist/maplibre-gl.css';
	import { m } from '$lib/paraglide/messages';
	import { formatDistance, formatElevation } from '$lib/format';
	import type { TrackPoint } from '../../routes/activities/[id]/+layout';

	let {
		coordinates,
		heightClass = 'h-96',
		track = [],
		hoverT = null,
		onHover
	}: {
		coordinates: [number, number][];
		heightClass?: string;
		// track: per-vertex data (index-aligned with coordinates) enabling the
		// snap-to-path marker + value overlay. Empty disables the feature.
		track?: TrackPoint[];
		// hoverT: externally-driven hover position (seconds offset), e.g. from the
		// streams chart — shows the marker at that point. null clears it.
		hoverT?: number | null;
		// onHover: fires with the hovered time (seconds offset) as the user moves
		// over the route, or null on leave. Lets a parent sync the streams chart.
		onHover?: (t: number | null) => void;
	} = $props();

	let container: HTMLDivElement;
	let map: MapLibreMap | undefined;
	let mapLoaded = $state(false);

	let hoverPoint = $state<TrackPoint | null>(null);

	function formatClock(s: number): string {
		const total = Math.max(0, Math.round(s));
		const h = Math.floor(total / 3600);
		const mm = Math.floor((total % 3600) / 60);
		const ss = total % 60;
		if (h > 0) return `${h}:${mm.toString().padStart(2, '0')}:${ss.toString().padStart(2, '0')}`;
		return `${mm}:${ss.toString().padStart(2, '0')}`;
	}

	// Nearest track vertex to a geographic point (linear scan; track is small at
	// the downsampled resolution the detail page requests).
	function nearestByLngLat(lng: number, lat: number): TrackPoint | null {
		let best: TrackPoint | null = null;
		let bestD = Infinity;
		for (const p of track) {
			const dx = p.lon - lng;
			const dy = p.lat - lat;
			const d = dx * dx + dy * dy;
			if (d < bestD) {
				bestD = d;
				best = p;
			}
		}
		return best;
	}

	// Nearest track vertex to a time offset (binary search — track is sorted by t).
	function nearestByTime(t: number): TrackPoint | null {
		if (track.length === 0) return null;
		let lo = 0;
		let hi = track.length - 1;
		while (lo < hi) {
			const mid = (lo + hi) >> 1;
			if (track[mid].t < t) lo = mid + 1;
			else hi = mid;
		}
		// lo is the first >= t; check the neighbour for the true nearest.
		if (lo > 0 && Math.abs(track[lo - 1].t - t) <= Math.abs(track[lo].t - t)) return track[lo - 1];
		return track[lo];
	}

	function setMarker(p: TrackPoint | null) {
		hoverPoint = p;
		const src = map?.getSource('hover-point') as maplibregl.GeoJSONSource | undefined;
		if (!src) return;
		src.setData({
			type: 'FeatureCollection',
			features:
				p == null
					? []
					: [{ type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [p.lon, p.lat] } }]
		});
	}

	onMount(() => {
		if (coordinates.length === 0) return;

		// Real street-level base map. CARTO's Voyager basemap is a light,
		// detailed style (roads + labels) that's free without an API key and
		// shows the route clearly; operators can point VITE_MAP_STYLE_URL at a
		// MapTiler/Mapbox style for richer tiles.
		const styleOverride = import.meta.env.VITE_MAP_STYLE_URL as string | undefined;
		const style = styleOverride ?? {
			version: 8 as const,
			sources: {
				basemap: {
					type: 'raster' as const,
					tiles: [
						'https://a.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}@2x.png',
						'https://b.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}@2x.png',
						'https://c.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}@2x.png'
					],
					tileSize: 256,
					attribution: '© OpenStreetMap contributors © CARTO'
				}
			},
			layers: [{ id: 'basemap', type: 'raster' as const, source: 'basemap' }]
		};

		map = new maplibregl.Map({
			container,
			style,
			center: coordinates[Math.floor(coordinates.length / 2)],
			zoom: 12,
			attributionControl: { compact: true }
		});

		map.on('load', () => {
			if (!map) return;

			map.addSource('route', {
				type: 'geojson',
				data: {
					type: 'Feature',
					properties: {},
					geometry: { type: 'LineString', coordinates }
				}
			});

			map.addLayer({
				id: 'route-casing',
				type: 'line',
				source: 'route',
				paint: {
					'line-color': '#0a0a0a',
					'line-width': 6,
					'line-opacity': 0.7
				},
				layout: { 'line-cap': 'round', 'line-join': 'round' }
			});

			map.addLayer({
				id: 'route-line',
				type: 'line',
				source: 'route',
				paint: { 'line-color': '#2dd4bf', 'line-width': 3 },
				layout: { 'line-cap': 'round', 'line-join': 'round' }
			});

			// Start (green) / finish (red) markers, ringed in white — mirrored by
			// the static course snapshot so both views read the same.
			map.addSource('endpoints', {
				type: 'geojson',
				data: {
					type: 'FeatureCollection',
					features: [
						{
							type: 'Feature',
							properties: { kind: 'start' },
							geometry: { type: 'Point', coordinates: coordinates[0] }
						},
						{
							type: 'Feature',
							properties: { kind: 'finish' },
							geometry: { type: 'Point', coordinates: coordinates[coordinates.length - 1] }
						}
					]
				}
			});
			map.addLayer({
				id: 'endpoint-markers',
				type: 'circle',
				source: 'endpoints',
				paint: {
					'circle-radius': 6,
					'circle-color': ['match', ['get', 'kind'], 'start', '#22c55e', '#ef4444'],
					'circle-stroke-color': '#ffffff',
					'circle-stroke-width': 2
				}
			});

			// Hover marker (driven by mouse or by an external hoverT).
			map.addSource('hover-point', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'hover-point',
				type: 'circle',
				source: 'hover-point',
				paint: {
					'circle-radius': 6,
					'circle-color': '#ffffff',
					'circle-stroke-color': '#2dd4bf',
					'circle-stroke-width': 3
				}
			});

			// Fit bounds to the full track with some padding.
			const bounds = coordinates.reduce(
				(b, c) => b.extend(c),
				new maplibregl.LngLatBounds(coordinates[0], coordinates[0])
			);
			map.fitBounds(bounds, { padding: 32, duration: 0 });

			mapLoaded = true;
			// Apply any hoverT that arrived before load.
			if (hoverT != null) setMarker(nearestByTime(hoverT));

			if (track.length > 0) {
				map.on('mousemove', (e) => {
					const p = nearestByLngLat(e.lngLat.lng, e.lngLat.lat);
					if (!p) return;
					// Only snap when the cursor is reasonably near the route (avoid a
					// marker jumping to the line when hovering empty map).
					const sp = map!.project([p.lon, p.lat]);
					const dist = Math.hypot(sp.x - e.point.x, sp.y - e.point.y);
					if (dist > 40) {
						setMarker(null);
						onHover?.(null);
						return;
					}
					setMarker(p);
					onHover?.(p.t);
				});
				map.on('mouseout', () => {
					setMarker(null);
					onHover?.(null);
				});
			}
		});
	});

	$effect(() => {
		// External hover (from the chart): move the marker, don't re-emit.
		// Read hoverT FIRST so it's always tracked as a dependency — otherwise an
		// early return on the initial (map-not-loaded) run would drop it and the
		// effect would never re-run when hoverT changes.
		const t = hoverT;
		if (!mapLoaded) return;
		if (t == null) setMarker(null);
		else setMarker(nearestByTime(t));
	});

	onDestroy(() => {
		map?.remove();
	});
</script>

<div class="relative {heightClass} w-full">
	<div bind:this={container} class="h-full w-full rounded-lg border border-zinc-800 bg-zinc-900"></div>

	{#if hoverPoint}
		<div
			class="pointer-events-none absolute left-2 top-2 z-10 rounded-md border border-zinc-700 bg-zinc-950/85 px-3 py-2 text-xs tabular-nums text-zinc-200 shadow-lg backdrop-blur"
		>
			<div class="flex flex-wrap gap-x-4 gap-y-1">
				<span><span class="text-zinc-500">t</span> {formatClock(hoverPoint.t)}</span>
				{#if hoverPoint.distM != null}
					<span><span class="text-zinc-500">dist</span> {formatDistance(hoverPoint.distM)}</span>
				{/if}
				{#if hoverPoint.eleM != null}
					<span><span class="text-zinc-500">ele</span> {formatElevation(hoverPoint.eleM)}</span>
				{/if}
				{#if hoverPoint.speedKmh != null}
					<span><span class="text-zinc-500">spd</span> {hoverPoint.speedKmh} km/h</span>
				{/if}
				{#if hoverPoint.hr != null}
					<span class="text-red-400">{hoverPoint.hr} bpm</span>
				{/if}
				{#if hoverPoint.power != null}
					<span class="text-orange-400">{hoverPoint.power} W</span>
				{/if}
				{#if hoverPoint.cadence != null}
					<span class="text-emerald-400">{hoverPoint.cadence} rpm</span>
				{/if}
			</div>
		</div>
	{/if}
</div>

{#if coordinates.length === 0}
	<p class="mt-2 text-xs text-zinc-500">{m.detail_map_no_track()}</p>
{/if}
