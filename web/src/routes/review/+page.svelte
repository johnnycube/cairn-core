<script lang="ts">
	import { onMount } from 'svelte';
	import SportIcon from '$lib/components/SportIcon.svelte';
	import { formatDateOnly } from '$lib/format';

	// Confidence-band review queue (brief §7.3): medium-confidence merges the
	// matcher flagged. The user confirms ("looks right" → clear flag) or opens
	// the activity to split it.
	type Item = {
		id: string;
		title: string;
		type: string;
		start_time: string;
		match_confidence: string;
	};

	let items = $state<Item[]>([]);
	let loaded = $state(false);
	let error = $state<string | null>(null);
	let busy = $state<string | null>(null);

	async function load() {
		try {
			const res = await fetch('/api/review-queue');
			if (!res.ok) throw new Error(await res.text());
			items = (await res.json()).items ?? [];
		} catch (e) {
			error = (e as Error).message || 'failed to load';
		}
		loaded = true;
	}
	onMount(load);

	async function confirm(id: string) {
		busy = id;
		try {
			const res = await fetch(`/api/activities/${id}/reviewed`, {
				method: 'POST',
				headers: { 'content-type': 'application/json' },
				body: '{}'
			});
			if (res.ok) items = items.filter((i) => i.id !== id);
		} finally {
			busy = null;
		}
	}
</script>

<svelte:head><title>Review · Cairn</title></svelte:head>

<div class="mx-auto max-w-3xl px-4 py-6">
	<h1 class="mb-1 text-xl font-semibold text-zinc-100">Review queue</h1>
	<p class="mb-6 text-sm text-zinc-500">
		Activities the merge engine combined from multiple sources with only
		medium confidence. Confirm the ones that look right, or open an activity to
		split a wrong merge.
	</p>

	{#if !loaded}
		<p class="text-sm text-zinc-600">Loading…</p>
	{:else if error}
		<p class="text-sm text-red-400">{error}</p>
	{:else if items.length === 0}
		<p class="text-sm text-zinc-500">Nothing to review — all merges are high-confidence. 🎉</p>
	{:else}
		<ul class="space-y-2">
			{#each items as it (it.id)}
				<li class="flex items-center gap-3 rounded-lg border border-zinc-800 bg-zinc-900/40 p-3 max-md:flex-wrap">
					<SportIcon type={it.type} size={18} />
					<div class="min-w-0 flex-1">
						<a href={`/activities/${it.id}`} class="truncate font-medium text-zinc-200 hover:text-accent-300">
							{it.title || 'Untitled activity'}
						</a>
						<div class="text-xs text-zinc-600">{formatDateOnly(it.start_time)}</div>
					</div>
					<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-300">medium</span>
					<a
						href={`/activities/${it.id}/manage`}
						class="rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-300 hover:border-accent-500"
					>
						Inspect
					</a>
					<button
						type="button"
						onclick={() => confirm(it.id)}
						disabled={busy === it.id}
						class="rounded bg-accent-600 px-2 py-1 text-xs font-medium text-white hover:bg-accent-500 disabled:opacity-50"
					>
						{busy === it.id ? '…' : 'Looks right'}
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</div>
