<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Map as MapLibreMap, Marker, NavigationControl, type LngLatLike } from 'maplibre-gl';
	import 'maplibre-gl/dist/maplibre-gl.css';
	import { basemapStyle } from '$lib/basemap';

	// A small click-to-pick map: clicking drops a marker and reports the
	// coordinates via onPick. Used to set a privacy-zone centre without typing
	// raw lat/lng. Reuses the same basemap as ActivityMap.
	let {
		lat = undefined,
		lng = undefined,
		onPick
	}: { lat?: number; lng?: number; onPick: (lat: number, lng: number) => void } = $props();

	let container: HTMLDivElement;
	let map: MapLibreMap | undefined;
	let marker: Marker | undefined;

	function place(lngLat: LngLatLike) {
		if (!map) return;
		if (!marker) marker = new Marker({ color: '#ef4444' }).setLngLat(lngLat).addTo(map);
		else marker.setLngLat(lngLat);
	}

	onMount(() => {
		const hasInitial = typeof lat === 'number' && typeof lng === 'number';
		map = new MapLibreMap({
			container,
			style: basemapStyle(),
			center: hasInitial ? [lng as number, lat as number] : [0, 25],
			zoom: hasInitial ? 13 : 1,
			attributionControl: { compact: true }
		});
		map.addControl(new NavigationControl({ showCompass: false }), 'top-right');
		if (hasInitial) place([lng as number, lat as number]);

		map.on('click', (e) => {
			place(e.lngLat);
			onPick(e.lngLat.lat, e.lngLat.lng);
		});
	});

	onDestroy(() => map?.remove());
</script>

<div bind:this={container} class="h-64 w-full overflow-hidden rounded-lg border border-zinc-800"></div>
