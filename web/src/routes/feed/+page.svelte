<script lang="ts">
	import { onMount } from 'svelte';
	import SportIcon from '$lib/components/SportIcon.svelte';
	import { formatDistance, formatDuration, formatRelativeDate } from '$lib/format';

	type Owner = { id: string; username: string; display_name: string; avatar_url: string };
	type Item = {
		id: string;
		title: string;
		type: string;
		discipline: string;
		start_time: string;
		distance_m: number | null;
		elevation_gain_m: number | null;
		elapsed_duration_s: number;
		start_place: string;
		start_location_redacted?: boolean;
		map_image_url?: string;
		owner: Owner;
	};

	let items = $state<Item[]>([]);
	let offset = $state(0);
	let hasMore = $state(false);
	let loading = $state(false);
	let error = $state<string | null>(null);

	async function load(reset: boolean) {
		loading = true;
		error = null;
		try {
			const off = reset ? 0 : offset;
			const res = await fetch(`/api/feed/following?limit=30&offset=${off}`);
			if (!res.ok) throw new Error((await res.text()).trim());
			const body = await res.json();
			const fresh = body.activities ?? [];
			items = off === 0 ? fresh : [...items, ...fresh];
			hasMore = body.has_more;
			offset = off + fresh.length;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loading = false;
		}
	}

	onMount(() => load(true));
</script>

<svelte:head><title>Feed · Cairn</title></svelte:head>

<div class="mx-auto max-w-2xl px-4 py-6">
	<h1 class="mb-4 text-xl font-semibold">Following</h1>

	{#if error}
		<p class="rounded bg-red-50 p-3 text-sm text-red-700">{error}</p>
	{/if}

	{#if items.length === 0 && !loading}
		<div class="rounded-lg border border-dashed p-8 text-center text-sm text-zinc-400">
			Nothing here yet. Follow other athletes to see their activities.
			<div class="mt-2"><a href="/athlete" class="text-accent-400 hover:underline">Find athletes →</a></div>
		</div>
	{/if}

	<div class="space-y-3">
		{#each items as a (a.id)}
			<article class="rounded-lg border bg-zinc-900/40 border border-zinc-800 p-4 shadow-sm">
				<div class="mb-2 flex items-center gap-2 text-sm text-zinc-400">
					<a href={`/u/${a.owner?.username}`} class="font-medium text-zinc-100 hover:underline">
						{a.owner?.display_name || a.owner?.username}
					</a>
					<span>·</span>
					<span>{formatRelativeDate(a.start_time)}</span>
				</div>
				<a href={`/a/${a.id}`} class="flex items-center gap-3 hover:opacity-90">
					<SportIcon type={a.type} size={24} />
					<div class="min-w-0 flex-1">
						<div class="truncate font-medium">{a.title || 'Untitled'}</div>
						{#if a.start_place}<div class="truncate text-xs text-zinc-400">{a.start_place}</div>{:else if a.start_location_redacted}<div class="truncate text-xs text-zinc-400">📍 Start location hidden</div>{/if}
					</div>
				</a>
				<div class="mt-3 flex divide-x divide-zinc-800 rounded-md border border-zinc-800 text-sm">
					<div class="flex-1 px-3 py-1.5">
						<div class="text-[10px] uppercase tracking-wide text-zinc-500">Distance</div>
						<div class="font-semibold tabular-nums">{formatDistance(a.distance_m ?? 0)}</div>
					</div>
					<div class="flex-1 px-3 py-1.5">
						<div class="text-[10px] uppercase tracking-wide text-zinc-500">Time</div>
						<div class="font-semibold tabular-nums">{formatDuration(a.elapsed_duration_s)}</div>
					</div>
					{#if a.elevation_gain_m}
						<div class="flex-1 px-3 py-1.5">
							<div class="text-[10px] uppercase tracking-wide text-zinc-500">Elevation</div>
							<div class="font-semibold tabular-nums">{Math.round(a.elevation_gain_m)} m</div>
						</div>
					{/if}
				</div>
				{#if a.map_image_url}
					<a href={`/a/${a.id}`} class="mt-3 block">
						<img
							src={a.map_image_url}
							alt="Course map"
							loading="lazy"
							class="aspect-[3/1] w-full rounded-md border border-zinc-800 object-cover"
							onerror={(e) => ((e.currentTarget as HTMLImageElement).style.display = 'none')}
						/>
					</a>
				{/if}
			</article>
		{/each}
	</div>

	{#if hasMore}
		<div class="mt-4 text-center">
			<button onclick={() => load(false)} disabled={loading}
				class="rounded border px-4 py-2 text-sm hover:bg-zinc-800 disabled:opacity-50">
				{loading ? 'Loading…' : 'Load more'}
			</button>
		</div>
	{/if}
</div>
