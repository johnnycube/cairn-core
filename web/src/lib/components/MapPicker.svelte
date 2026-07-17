<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import maplibregl, { type Map as MapLibreMap } from 'maplibre-gl';
	import 'maplibre-gl/dist/maplibre-gl.css';

	// A small click-to-pick map: clicking drops a marker and reports the
	// coordinates via onPick. Used to set a privacy-zone centre without typing
	// raw lat/lng. Reuses the same key-less CARTO raster basemap as ActivityMap.
	let {
		lat = undefined,
		lng = undefined,
		onPick
	}: { lat?: number; lng?: number; onPick: (lat: number, lng: number) => void } = $props();

	let container: HTMLDivElement;
	let map: MapLibreMap | undefined;
	let marker: maplibregl.Marker | undefined;

	function place(lngLat: maplibregl.LngLatLike) {
		if (!map) return;
		if (!marker) marker = new maplibregl.Marker({ color: '#ef4444' }).setLngLat(lngLat).addTo(map);
		else marker.setLngLat(lngLat);
	}

	onMount(() => {
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

		const hasInitial = typeof lat === 'number' && typeof lng === 'number';
		map = new maplibregl.Map({
			container,
			style,
			center: hasInitial ? [lng as number, lat as number] : [0, 25],
			zoom: hasInitial ? 13 : 1,
			attributionControl: { compact: true }
		});
		map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
		if (hasInitial) place([lng as number, lat as number]);

		map.on('click', (e) => {
			place(e.lngLat);
			onPick(e.lngLat.lat, e.lngLat.lng);
		});
	});

	onDestroy(() => map?.remove());
</script>

<div bind:this={container} class="h-64 w-full overflow-hidden rounded-lg border border-zinc-800"></div>
