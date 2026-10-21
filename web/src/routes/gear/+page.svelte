<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { m } from '#lib/paraglide/messages.js';
	import { clients } from '#lib/connect.js';
	import { ActivityType } from '#lib/proto/cairn/v1/activity_pb.js';
	import { enumValue } from '#lib/proto-helpers.js';
	import { ACTIVITY_TYPES } from '#lib/activity-types.js';
	import { formatDistance, formatDuration, formatDateOnly } from '#lib/format.js';
	import { GEAR_KINDS, gearLabel, kindValue, type GearRefView, type GearView } from '#lib/gear.js';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	const active = $derived(data.gear.filter((g) => !g.retired));
	const retired = $derived(data.gear.filter((g) => g.retired));
	const defaultsByType = $derived(
		Object.fromEntries(data.defaults.map((d) => [d.activityType, d.gearId])) as Record<
			string,
			string
		>
	);
	const defaultTypesByGear = $derived(
		data.defaults.reduce<Record<string, string[]>>((acc, d) => {
			(acc[d.gearId] ??= []).push(d.activityType);
			return acc;
		}, {})
	);

	let error = $state<string | null>(null);
	let busy = $state(false);

	// Pending decisions: "later" only hides the card for this visit; the
	// record stays pending on the server until a real decision.
	let dismissed = $state<Record<string, boolean>>({});
	let mapTarget = $state<Record<string, string>>({});
	function refKey(r: GearRefView): string {
		return `${r.provider}:${r.externalAccountId}:${r.externalId}`;
	}
	const openPending = $derived(data.pending.filter((r) => !dismissed[refKey(r)]));

	async function run(fn: () => Promise<unknown>) {
		busy = true;
		error = null;
		try {
			await fn();
			await invalidateAll();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	function createFromRef(r: GearRefView) {
		return run(() =>
			clients.gear.createGearFromRef({
				provider: r.provider,
				externalAccountId: r.externalAccountId,
				externalId: r.externalId
			})
		);
	}
	function linkRef(r: GearRefView, gearId: string) {
		if (!gearId) return;
		return run(() =>
			clients.gear.linkGearRef({
				gearId,
				provider: r.provider,
				externalAccountId: r.externalAccountId,
				externalId: r.externalId
			})
		);
	}

	// Create form.
	let showCreate = $state(false);
	let cKind = $state('bike');
	let cName = $state('');
	let cBrand = $state('');
	let cModel = $state('');
	let cPurchase = $state('');
	let cNotes = $state('');
	function createGear() {
		return run(async () => {
			await clients.gear.createGear({
				kind: kindValue(cKind),
				name: cName.trim(),
				brand: cBrand.trim(),
				model: cModel.trim(),
				purchaseDate: cPurchase,
				notes: cNotes
			});
			cName = cBrand = cModel = cPurchase = cNotes = '';
			showCreate = false;
		});
	}

	// Inline edit of one gear at a time.
	let editId = $state<string | null>(null);
	let eKind = $state('bike');
	let eName = $state('');
	let eBrand = $state('');
	let eModel = $state('');
	let ePurchase = $state('');
	let eNotes = $state('');
	function startEdit(g: GearView) {
		editId = g.id;
		eKind = g.kind;
		eName = g.name;
		eBrand = g.brand;
		eModel = g.model;
		ePurchase = g.purchaseDate;
		eNotes = g.notes;
	}
	function saveEdit(g: GearView) {
		return run(async () => {
			await clients.gear.updateGear({
				id: g.id,
				fieldMask: ['name', 'brand', 'model', 'notes', 'purchase_date', 'kind'],
				name: eName.trim(),
				brand: eBrand.trim(),
				model: eModel.trim(),
				notes: eNotes,
				purchaseDate: ePurchase,
				kind: kindValue(eKind)
			});
			editId = null;
		});
	}
	function setRetired(g: GearView, retired: boolean) {
		return run(() => clients.gear.updateGear({ id: g.id, fieldMask: ['retired'], retired }));
	}
	function remove(g: GearView) {
		const n = g.activityCount;
		if (
			!confirm(
				`Delete "${g.name}"? ${n} ${n === 1 ? 'activity keeps' : 'activities keep'} no gear afterwards.`
			)
		)
			return;
		return run(() => clients.gear.deleteGear({ id: g.id }));
	}

	let mergeInto = $state<Record<string, string>>({});
	function merge(g: GearView) {
		const to = mergeInto[g.id];
		if (!to) return;
		const target = data.gear.find((x) => x.id === to);
		if (
			!confirm(
				`Merge "${g.name}" into "${target?.name ?? to}"? Its activities and provider records move over; this cannot be undone.`
			)
		)
			return;
		return run(() => clients.gear.mergeGear({ fromId: g.id, toId: to }));
	}

	function setDefault(type: string, gearId: string) {
		return run(() =>
			gearId
				? clients.gear.setGearDefault({ activityType: enumValue(ActivityType, type), gearId })
				: clients.gear.clearGearDefault({ activityType: enumValue(ActivityType, type) })
		);
	}

	const field = 'mt-1 w-full rounded border border-zinc-800 bg-zinc-900/60 px-3 py-2 text-sm';
	const btn =
		'rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:border-accent-500 hover:text-accent-300 disabled:opacity-50';
	const btnPrimary =
		'rounded border border-accent-500 bg-accent-500/20 px-2.5 py-1 text-xs text-accent-300 hover:bg-accent-500/30 disabled:opacity-50';
	const kindLabel = (k: string) => GEAR_KINDS.find((x) => x.value === k)?.label ?? k;
</script>

<section class="space-y-8">
	<header class="flex items-start justify-between gap-4 max-md:flex-wrap">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight">{m.gear_title()}</h1>
			<p class="mt-1 text-sm text-zinc-400">{m.gear_intro()}</p>
		</div>
		<button type="button" class={btnPrimary} onclick={() => (showCreate = !showCreate)}>
			{showCreate ? m.gear_cancel() : m.gear_add()}
		</button>
	</header>

	{#if error}
		<div class="rounded border border-red-700/50 bg-red-950/30 px-3 py-2 text-xs text-red-300">
			{error}
		</div>
	{/if}

	{#if openPending.length > 0}
		<!-- Provider records waiting for a decision (docs/gear-v0.4.0.md). -->
		<div class="rounded-xl border border-amber-700/50 bg-amber-950/20 p-5">
			<h2 class="text-lg font-semibold text-amber-200">{m.gear_pending_title()}</h2>
			<p class="mt-1 text-sm text-amber-200/70">{m.gear_pending_intro()}</p>
			<ul class="mt-4 space-y-3">
				{#each openPending as r (refKey(r))}
					<li class="rounded-lg border border-zinc-800 bg-zinc-900/60 p-4">
						<div class="flex flex-wrap items-center gap-2 text-sm">
							<span
								class="rounded bg-zinc-800 px-1.5 py-0.5 text-[11px] uppercase tracking-wide text-zinc-300"
								>{r.provider}</span
							>
							<span class="font-medium">{r.providerName || r.externalId}</span>
							<span class="text-zinc-500">· {kindLabel(r.kind)}</span>
							{#if r.brand || r.model}<span class="text-zinc-500"
									>· {[r.brand, r.model].filter(Boolean).join(' ')}</span
								>{/if}
							{#if r.providerDistanceM != null}<span class="text-zinc-500"
									>· {formatDistance(r.providerDistanceM)}</span
								>{/if}
							{#if r.retired}<span class="text-zinc-500">· retired</span>{/if}
						</div>
						<div class="mt-3 flex flex-wrap items-center gap-2">
							<button
								type="button"
								class={btnPrimary}
								disabled={busy}
								onclick={() => createFromRef(r)}
							>
								{m.gear_pending_create()}
							</button>
							<select
								bind:value={mapTarget[refKey(r)]}
								class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300"
							>
								<option value="">{m.gear_pending_map_placeholder()}</option>
								{#each data.gear as g (g.id)}
									<option value={g.id}>{gearLabel(g)}{g.retired ? ' (retired)' : ''}</option>
								{/each}
							</select>
							<button
								type="button"
								class={btn}
								disabled={busy || !mapTarget[refKey(r)]}
								onclick={() => linkRef(r, mapTarget[refKey(r)] ?? '')}
							>
								{m.gear_pending_map()}
							</button>
							<button
								type="button"
								class="ml-auto text-xs text-zinc-500 hover:text-zinc-300"
								onclick={() => (dismissed = { ...dismissed, [refKey(r)]: true })}
							>
								{m.gear_pending_later()}
							</button>
						</div>
					</li>
				{/each}
			</ul>
		</div>
	{/if}

	{#if showCreate}
		<form
			class="space-y-4 rounded-xl border border-zinc-800 bg-zinc-900/40 p-5"
			onsubmit={(e) => {
				e.preventDefault();
				createGear();
			}}
		>
			<div class="grid grid-cols-2 gap-4 max-md:grid-cols-1">
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-kind"
						>{m.gear_field_kind()}</label
					>
					<select id="c-kind" bind:value={cKind} class={field}>
						{#each GEAR_KINDS as k (k.value)}<option value={k.value}>{k.label}</option>{/each}
					</select>
				</div>
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-name"
						>{m.gear_field_name()}</label
					>
					<input id="c-name" bind:value={cName} required class={field} />
				</div>
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-brand"
						>{m.gear_field_brand()}</label
					>
					<input id="c-brand" bind:value={cBrand} class={field} />
				</div>
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-model"
						>{m.gear_field_model()}</label
					>
					<input id="c-model" bind:value={cModel} class={field} />
				</div>
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-purchase"
						>{m.gear_field_purchase()}</label
					>
					<input
						id="c-purchase"
						type="date"
						bind:value={cPurchase}
						class="{field} [color-scheme:dark]"
					/>
				</div>
				<div>
					<label class="block text-xs uppercase tracking-wide text-zinc-500" for="c-notes"
						>{m.gear_field_notes()}</label
					>
					<input id="c-notes" bind:value={cNotes} class={field} />
				</div>
			</div>
			<button type="submit" disabled={busy || cName.trim() === ''} class={btnPrimary}
				>{m.gear_save()}</button
			>
		</form>
	{/if}

	<div>
		<h2 class="text-lg font-semibold">{m.gear_section_active()}</h2>
		{#if active.length === 0}
			<p class="mt-2 text-sm text-zinc-500">{m.gear_empty()}</p>
		{:else}
			<ul class="mt-3 space-y-3">
				{#each active as g (g.id)}
					<li class="rounded-lg border border-zinc-800 bg-zinc-900/40 p-4">
						{#if editId === g.id}
							<form
								class="space-y-3"
								onsubmit={(e) => {
									e.preventDefault();
									saveEdit(g);
								}}
							>
								<div class="grid grid-cols-3 gap-3 max-md:grid-cols-1">
									<select bind:value={eKind} class={field} aria-label={m.gear_field_kind()}>
										{#each GEAR_KINDS as k (k.value)}<option value={k.value}>{k.label}</option
											>{/each}
									</select>
									<input
										bind:value={eName}
										required
										class={field}
										placeholder={m.gear_field_name()}
									/>
									<input bind:value={eBrand} class={field} placeholder={m.gear_field_brand()} />
									<input bind:value={eModel} class={field} placeholder={m.gear_field_model()} />
									<input type="date" bind:value={ePurchase} class="{field} [color-scheme:dark]" />
									<input bind:value={eNotes} class={field} placeholder={m.gear_field_notes()} />
								</div>
								<div class="flex gap-2">
									<button type="submit" disabled={busy} class={btnPrimary}>{m.gear_save()}</button>
									<button type="button" class={btn} onclick={() => (editId = null)}
										>{m.gear_cancel()}</button
									>
								</div>
							</form>
						{:else}
							<div class="flex items-start justify-between gap-4 max-md:flex-wrap">
								<div class="min-w-0">
									<div class="flex flex-wrap items-center gap-2">
										<span
											class="rounded bg-zinc-800 px-1.5 py-0.5 text-[11px] uppercase tracking-wide text-zinc-400"
											>{kindLabel(g.kind)}</span
										>
										<span class="font-medium">{g.name}</span>
										{#if g.brand || g.model}<span class="text-sm text-zinc-500"
												>{[g.brand, g.model].filter(Boolean).join(' ')}</span
											>{/if}
										{#each g.refs as r (r.provider + r.externalId)}
											<span
												class="rounded bg-sky-500/15 px-1.5 py-0.5 text-[11px] uppercase tracking-wide text-sky-300"
												title={`${r.provider}: ${r.providerName || r.externalId}`}
												>{r.provider}</span
											>
										{/each}
										{#each defaultTypesByGear[g.id] ?? [] as t (t)}
											<span
												class="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[11px] uppercase tracking-wide text-emerald-300"
											>
												{m.gear_default_badge({ type: t })}
											</span>
										{/each}
									</div>
									<p class="mt-1 text-sm text-zinc-400">
										{formatDistance(g.totalDistanceM)} · {formatDuration(g.totalMovingS)} ·
										<a
											href={`/activities?gear_id=${g.id}`}
											class="text-accent-400 hover:text-accent-300"
										>
											{m.gear_activity_count({ count: g.activityCount })}
										</a>
										{#if g.purchaseDate}· {m.gear_since({
												date: formatDateOnly(g.purchaseDate)
											})}{/if}
									</p>
									{#if g.notes}<p class="mt-1 text-xs text-zinc-500">{g.notes}</p>{/if}
								</div>
								<div class="flex shrink-0 flex-wrap items-center gap-2">
									<button type="button" class={btn} onclick={() => startEdit(g)}
										>{m.gear_edit()}</button
									>
									<button
										type="button"
										class={btn}
										disabled={busy}
										onclick={() => setRetired(g, true)}>{m.gear_retire()}</button
									>
									<button type="button" class={btn} disabled={busy} onclick={() => remove(g)}
										>{m.gear_delete()}</button
									>
								</div>
							</div>
							{#if data.gear.length > 1}
								<div class="mt-3 flex items-center gap-2 text-xs text-zinc-500">
									<span>{m.gear_merge_label()}</span>
									<select
										bind:value={mergeInto[g.id]}
										class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300"
									>
										<option value="">—</option>
										{#each data.gear.filter((x) => x.id !== g.id) as x (x.id)}
											<option value={x.id}>{gearLabel(x)}{x.retired ? ' (retired)' : ''}</option>
										{/each}
									</select>
									<button
										type="button"
										class={btn}
										disabled={busy || !mergeInto[g.id]}
										onclick={() => merge(g)}
									>
										{m.gear_merge()}
									</button>
								</div>
							{/if}
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</div>

	{#if data.gear.length > 0}
		<div class="rounded-xl border border-zinc-800 bg-zinc-900/40 p-5">
			<h2 class="text-lg font-semibold">{m.gear_section_defaults()}</h2>
			<p class="mt-1 text-sm text-zinc-500">{m.gear_defaults_intro()}</p>
			<div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
				{#each ACTIVITY_TYPES as t (t)}
					<label class="flex items-center justify-between gap-2 text-sm">
						<span class="capitalize text-zinc-300">{t}</span>
						<select
							value={defaultsByType[t] ?? ''}
							disabled={busy}
							onchange={(e) => setDefault(t, (e.currentTarget as HTMLSelectElement).value)}
							class="w-48 rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300"
						>
							<option value="">—</option>
							{#each active as g (g.id)}
								<option value={g.id}>{gearLabel(g)}</option>
							{/each}
						</select>
					</label>
				{/each}
			</div>
		</div>
	{/if}

	{#if retired.length > 0}
		<details class="rounded-xl border border-zinc-800 p-5">
			<summary class="cursor-pointer text-lg font-semibold"
				>{m.gear_section_retired()} ({retired.length})</summary
			>
			<ul class="mt-3 space-y-2">
				{#each retired as g (g.id)}
					<li class="flex items-center justify-between gap-4 text-sm max-md:flex-wrap">
						<span>
							<span class="font-medium text-zinc-300">{g.name}</span>
							<span class="text-zinc-500">
								· {formatDistance(g.totalDistanceM)} · {m.gear_activity_count({
									count: g.activityCount
								})}</span
							>
							{#if g.retiredAt}<span class="text-zinc-600">
									· {formatDateOnly(g.retiredAt)}</span
								>{/if}
						</span>
						<span class="flex gap-2">
							<button type="button" class={btn} disabled={busy} onclick={() => setRetired(g, false)}
								>{m.gear_unretire()}</button
							>
							<button type="button" class={btn} disabled={busy} onclick={() => remove(g)}
								>{m.gear_delete()}</button
							>
						</span>
					</li>
				{/each}
			</ul>
		</details>
	{/if}

	<p class="text-xs text-zinc-600">{m.gear_sync_hint()}</p>
</section>
