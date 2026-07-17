<script lang="ts">
	import ActivityMap from '$lib/components/ActivityMap.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	const a = $derived(data.activity);
	const coordinates = $derived(data.stream?.coordinates ?? []);
	const track = $derived(data.stream?.track ?? []);
</script>

<section class="flex h-[calc(100dvh-4rem)] max-md:h-[calc(100dvh-7.5rem)] flex-col gap-3">
	<header class="flex items-center justify-between gap-4">
		<div class="min-w-0">
			<a href={`/activities/${a.id}`} class="text-xs text-accent-400 hover:text-accent-300">
				← {a.title || 'Activity'}
			</a>
			<h1 class="truncate text-xl font-semibold tracking-tight">Route</h1>
		</div>
	</header>

	{#if coordinates.length > 0}
		<div class="min-h-0 flex-1">
			<ActivityMap {coordinates} {track} heightClass="h-full" />
		</div>
	{:else}
		<div
			class="flex flex-1 items-center justify-center rounded-lg border border-dashed border-zinc-700 bg-zinc-900/30 text-sm text-zinc-400"
		>
			This activity has no GPS track.
		</div>
	{/if}
</section>
