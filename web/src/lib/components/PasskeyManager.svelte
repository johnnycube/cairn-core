<script lang="ts">
	import { onMount } from 'svelte';
	import { formatDate } from '$lib/format';
	import { registerPasskey, supported as passkeysSupported } from '$lib/webauthn';

	type Passkey = {
		id: string;
		name: string;
		created_at: string;
		last_used_at: string | null;
	};

	let passkeys = $state<Passkey[]>([]);
	let busy = $state(false);
	let error = $state<string | null>(null);
	const supported = passkeysSupported();

	async function load() {
		try {
			const res = await fetch('/api/passkeys');
			if (res.ok) passkeys = (await res.json()).passkeys ?? [];
		} catch {
			/* ignore */
		}
	}
	onMount(load);

	async function add() {
		const name = (prompt('Name this passkey (e.g. "MacBook Touch ID")') ?? '').trim();
		if (name === '') return;
		busy = true;
		error = null;
		try {
			await registerPasskey(name);
			await load();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	async function rename(p: Passkey) {
		const name = (prompt('Rename passkey', p.name) ?? '').trim();
		if (name === '' || name === p.name) return;
		await fetch(`/api/passkeys/${p.id}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name })
		});
		await load();
	}

	async function remove(p: Passkey) {
		if (!confirm(`Remove passkey "${p.name}"?`)) return;
		await fetch(`/api/passkeys/${p.id}`, { method: 'DELETE' });
		await load();
	}
</script>

<section class="rounded-lg border border-zinc-800 bg-zinc-900/40 p-5">
	<div class="mb-1 flex items-center justify-between">
		<h2 class="text-sm font-medium text-zinc-300">Passkeys</h2>
		{#if supported}
			<button
				type="button"
				onclick={add}
				disabled={busy}
				class="rounded border border-accent-500 bg-accent-500/20 px-3 py-1.5 text-xs text-accent-300 hover:bg-accent-500/30 disabled:opacity-50"
			>
				{busy ? 'Waiting…' : '+ Add passkey'}
			</button>
		{/if}
	</div>
	<p class="mb-3 text-xs text-zinc-500">
		Sign in without a password using Touch ID, Windows Hello, or a security key. Passkeys are
		phishing-resistant and never leave your device.
	</p>

	{#if error}
		<div class="mb-3 rounded border border-red-700/50 bg-red-950/30 px-3 py-2 text-xs text-red-300">{error}</div>
	{/if}

	{#if !supported}
		<p class="text-xs text-zinc-600">This browser doesn’t support passkeys.</p>
	{:else if passkeys.length === 0}
		<p class="text-xs text-zinc-600">No passkeys yet.</p>
	{:else}
		<ul class="divide-y divide-zinc-800 rounded border border-zinc-800">
			{#each passkeys as p (p.id)}
				<li class="flex items-center justify-between gap-4 px-3 py-2 text-xs max-md:flex-wrap">
					<div>
						<span class="font-medium text-zinc-200">{p.name}</span>
						<div class="mt-0.5 text-zinc-600">
							added {formatDate(p.created_at)}
							{#if p.last_used_at}· last used {formatDate(p.last_used_at)}{:else}· never used{/if}
						</div>
					</div>
					<div class="flex shrink-0 gap-2">
						<button type="button" onclick={() => rename(p)} class="text-zinc-400 hover:text-zinc-200">Rename</button>
						<button type="button" onclick={() => remove(p)} class="text-zinc-400 hover:text-red-300">Remove</button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</section>
