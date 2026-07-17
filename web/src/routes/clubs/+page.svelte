<script lang="ts">
	import { onMount } from 'svelte';

	type Club = { id: string; slug: string; name: string; is_public: boolean; member_count: number };
	let clubs = $state<Club[]>([]);
	let error = $state<string | null>(null);
	let showCreate = $state(false);
	let form = $state({ name: '', description: '', is_public: true });
	let creating = $state(false);

	async function load() {
		error = null;
		try {
			const res = await fetch('/api/clubs');
			if (!res.ok) throw new Error((await res.text()).trim());
			clubs = (await res.json()).clubs ?? [];
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function create() {
		if (!form.name.trim()) return;
		creating = true;
		try {
			const res = await fetch('/api/clubs', {
				method: 'POST', headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(form)
			});
			if (res.ok) {
				const b = await res.json();
				window.location.href = `/clubs/${b.slug}`;
			} else {
				error = (await res.text()).trim();
			}
		} finally {
			creating = false;
		}
	}

	onMount(load);
</script>

<svelte:head><title>Clubs · Cairn</title></svelte:head>

<div class="mx-auto max-w-2xl px-4 py-6">
	<div class="mb-4 flex items-center justify-between">
		<h1 class="text-xl font-semibold">Clubs</h1>
		<button onclick={() => (showCreate = !showCreate)}
			class="rounded bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700">
			{showCreate ? 'Cancel' : 'New club'}
		</button>
	</div>

	{#if error}<p class="mb-3 rounded bg-red-50 p-3 text-sm text-red-700">{error}</p>{/if}

	{#if showCreate}
		<div class="mb-4 space-y-3 rounded-lg border p-4">
			<input bind:value={form.name} placeholder="Club name" class="w-full rounded border px-3 py-2 text-sm" />
			<textarea bind:value={form.description} placeholder="Description (optional)" rows="2" class="w-full rounded border px-3 py-2 text-sm"></textarea>
			<label class="flex items-center gap-2 text-sm">
				<input type="checkbox" bind:checked={form.is_public} /> Public (anyone can find &amp; join)
			</label>
			<button onclick={create} disabled={creating || !form.name.trim()}
				class="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
				{creating ? 'Creating…' : 'Create club'}
			</button>
		</div>
	{/if}

	{#if clubs.length === 0}
		<p class="rounded-lg border border-dashed p-8 text-center text-sm text-gray-500">No clubs yet. Create one!</p>
	{/if}
	<div class="space-y-2">
		{#each clubs as c (c.id)}
			<a href={`/clubs/${c.slug}`} class="flex items-center justify-between rounded-lg border bg-white p-4 shadow-sm hover:opacity-90">
				<div>
					<div class="font-medium">{c.name}</div>
					<div class="text-xs text-gray-500">{c.member_count} {c.member_count === 1 ? 'member' : 'members'} · {c.is_public ? 'Public' : 'Private'}</div>
				</div>
				<span class="text-gray-400">→</span>
			</a>
		{/each}
	</div>
</div>
