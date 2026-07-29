<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import { m } from '$lib/paraglide/messages';
	import SportIcon from '$lib/components/SportIcon.svelte';
	import { formatDistance, formatDuration, formatElevation, formatRelativeDate } from '$lib/format';
	import { prefs } from '$lib/prefs.svelte';

	type Facet = { value: string; count: number };
	type FeedActivity = {
		id: string;
		title: string;
		type: string;
		discipline: string;
		start_time: string;
		timezone: string;
		elapsed_duration_s: number;
		distance_m: number | null;
		elevation_gain_m: number | null;
		tss: number | null;
		start_place: string;
	};
	type Feed = {
		total: number;
		matched: number;
		facets: { types: Facet[]; disciplines: Facet[]; years?: Facet[] };
		activities: FeedActivity[];
		has_more: boolean;
	};
	const PAGE_SIZE = 50;

	const SORTS = [
		{ value: 'date_desc', label: 'Newest first' },
		{ value: 'date_asc', label: 'Oldest first' },
		{ value: 'distance_desc', label: 'Longest distance' },
		{ value: 'duration_desc', label: 'Longest duration' },
		{ value: 'elevation_desc', label: 'Most climbing' },
		{ value: 'speed_desc', label: 'Fastest average' }
	];

	// Filter + sort state.
	let typeFilter = $state('');
	let disciplineFilter = $state('');
	let sort = $state('date_desc');

	// Date-range filter. Seeded from the URL (?from=&to=, half-open) so the
	// dashboard's "This week" / "Last week" blocks can deep-link into a
	// pre-filtered activities view, then editable in place. Half-open: from
	// inclusive, to exclusive.
	let fromDate = $state(page.url.searchParams.get('from') ?? '');
	let toDate = $state(page.url.searchParams.get('to') ?? '');

	// Quick date presets — "last N days" sets from = today-N, to = open.
	const DATE_PRESETS = [
		{ label: '7d', days: 7 },
		{ label: '30d', days: 30 },
		{ label: '90d', days: 90 },
		{ label: '1y', days: 365 }
	];
	let activePreset = $state<number | null>(null);
	function applyPreset(days: number) {
		yearFilter = '';
		if (activePreset === days) {
			activePreset = null;
			fromDate = '';
			toDate = '';
			return;
		}
		activePreset = days;
		const d = new Date();
		d.setDate(d.getDate() - days);
		fromDate = d.toISOString().slice(0, 10);
		toDate = '';
	}

	// Year filter — sugar over the half-open from/to range the API already
	// supports. Options come from the (unfiltered) years facet.
	let yearFilter = $state('');
	function applyYear() {
		activePreset = null;
		if (yearFilter) {
			fromDate = `${yearFilter}-01-01`;
			toDate = `${Number(yearFilter) + 1}-01-01`;
		} else {
			fromDate = '';
			toDate = '';
		}
	}

	// Tri-state classification flags: '' = any, 'true', 'false'.
	const FLAGS = [
		{ key: 'virtual', label: 'Virtual' },
		{ key: 'ebike', label: 'E-bike' },
		{ key: 'commute', label: 'Commute' },
		{ key: 'race', label: 'Race' }
	] as const;
	let flags = $state<Record<string, string>>({ virtual: '', ebike: '', commute: '', race: '' });
	function cycleFlag(key: string) {
		flags[key] = flags[key] === '' ? 'true' : flags[key] === 'true' ? 'false' : '';
	}

	// Numeric range filters. Entered in the user's display units (metric or
	// imperial, per prefs — same constants as $lib/format), converted to the SI
	// query params the API expects. Empty string = unbounded.
	const M_PER_MILE = 1609.344;
	const FT_PER_M = 3.28084;
	const RANGE_KEYS = ['distance', 'duration', 'elevation', 'speed', 'hr', 'power'] as const;
	const RANGES = $derived.by(() => {
		const imperial = prefs.units === 'imperial';
		return [
			{
				key: 'distance',
				label: 'Distance',
				unit: imperial ? 'mi' : 'km',
				toSI: (v: number) => (imperial ? v * M_PER_MILE : v * 1000),
				param: 'distance_{}_m'
			},
			{
				key: 'duration',
				label: 'Duration',
				unit: 'h',
				toSI: (v: number) => v * 3600,
				param: 'duration_{}_s'
			},
			{
				key: 'elevation',
				label: 'Elevation gain',
				unit: imperial ? 'ft' : 'm',
				toSI: (v: number) => (imperial ? v / FT_PER_M : v),
				param: 'elevation_{}_m'
			},
			{
				key: 'speed',
				label: 'Avg speed',
				unit: imperial ? 'mph' : 'km/h',
				toSI: (v: number) => (imperial ? (v * M_PER_MILE) / 3600 : v / 3.6),
				param: 'speed_{}_mps'
			},
			{
				key: 'hr',
				label: 'Avg heart rate',
				unit: 'bpm',
				toSI: (v: number) => v,
				param: 'hr_{}_bpm'
			},
			{ key: 'power', label: 'Avg power', unit: 'W', toSI: (v: number) => v, param: 'power_{}_w' }
		];
	});
	let ranges = $state<Record<string, { min: string; max: string }>>(
		Object.fromEntries(RANGE_KEYS.map((k) => [k, { min: '', max: '' }]))
	);
	let moreFilters = $state(false);

	// Debounce free-text numeric/date input so we don't re-query per keystroke.
	// The effect also runs once on mount — skip that, or the initial load
	// double-fetches (once immediately, once when the timer fires).
	let debounced = $state(0);
	let debounceTimer: ReturnType<typeof setTimeout> | undefined;
	let debounceInit = false;
	function bumpDebounced() {
		clearTimeout(debounceTimer);
		debounceTimer = setTimeout(() => (debounced += 1), 350);
	}
	$effect(() => {
		JSON.stringify(ranges);
		fromDate;
		toDate;
		if (!debounceInit) {
			debounceInit = true;
			return;
		}
		bumpDebounced();
	});

	let feed = $state<Feed | null>(null);
	let items = $state<FeedActivity[]>([]);
	let pageNum = $state(1);
	let loading = $state(false);

	const totalPages = $derived(feed ? Math.max(1, Math.ceil(feed.matched / PAGE_SIZE)) : 1);

	// Windowed pager: first, last, current ±1, with ellipsis gaps.
	const pageList = $derived.by(() => {
		const wanted = [1, pageNum - 1, pageNum, pageNum + 1, totalPages];
		const pages = [...new Set(wanted)].filter((p) => p >= 1 && p <= totalPages).sort((a, b) => a - b);
		const out: (number | '…')[] = [];
		let prev = 0;
		for (const p of pages) {
			if (p - prev > 1) out.push('…');
			out.push(p);
			prev = p;
		}
		return out;
	});

	function humanize(v: string): string {
		return v.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
	}

	// Filter changes can fire faster than responses arrive; abort the in-flight
	// request and drop stale responses so the list never flashes old results.
	let feedSeq = 0;
	let feedAbort: AbortController | null = null;

	// Takes the target page explicitly (not pageNum) so the filter $effect
	// doesn't track pageNum and re-fire on page navigation.
	async function loadFeed(p: number) {
		const seq = ++feedSeq;
		feedAbort?.abort();
		const ctrl = new AbortController();
		feedAbort = ctrl;
		loading = true;
		try {
			const q = new URLSearchParams({
				sort,
				limit: String(PAGE_SIZE),
				offset: String((p - 1) * PAGE_SIZE)
			});
			if (typeFilter) q.set('type', typeFilter);
			if (disciplineFilter) q.set('discipline', disciplineFilter);
			if (fromDate) q.set('from', fromDate);
			if (toDate) q.set('to', toDate);
			for (const f of FLAGS) {
				if (flags[f.key]) q.set(f.key, flags[f.key]);
			}
			for (const r of RANGES) {
				for (const bound of ['min', 'max'] as const) {
					const raw = ranges[r.key][bound];
					const v = raw === '' ? NaN : Number(raw);
					if (!Number.isNaN(v) && v >= 0) {
						q.set(r.param.replace('{}', bound), String(r.toSI(v)));
					}
				}
			}
			const res = await fetch(`/api/activities/feed?${q.toString()}`, { signal: ctrl.signal });
			if (res.ok && seq === feedSeq) {
				const data: Feed = await res.json();
				items = data.activities;
				feed = data;
			}
		} catch {
			/* ignore (includes aborts) */
		} finally {
			if (seq === feedSeq) loading = false;
		}
	}

	function goToPage(p: number) {
		if (p < 1 || p > totalPages || p === pageNum) return;
		pageNum = p;
		loadFeed(p);
		window.scrollTo({ top: 0, behavior: 'smooth' });
	}

	// Re-query (reset to page 1) whenever a filter or sort changes. Text-input
	// filters (dates, ranges) arrive via the debounced counter.
	$effect(() => {
		typeFilter;
		disciplineFilter;
		sort;
		JSON.stringify(flags);
		debounced;
		pageNum = 1;
		loadFeed(1);
	});

	function clearFilters() {
		typeFilter = '';
		disciplineFilter = '';
		fromDate = '';
		toDate = '';
		activePreset = null;
		yearFilter = '';
		flags = { virtual: '', ebike: '', commute: '', race: '' };
		ranges = Object.fromEntries(RANGE_KEYS.map((k) => [k, { min: '', max: '' }]));
	}

	// Facets respect every filter except their own dimension, so another filter
	// can shrink them mid-interaction. Keep a row visible once it has appeared,
	// or the bar jumps around ("flickers") while filtering.
	let showSports = $state(false);
	let showDisciplines = $state(false);
	$effect(() => {
		if (feed && feed.facets.types.length > 1) showSports = true;
		if (feed && feed.facets.disciplines.length > 0) showDisciplines = true;
	});

	const rangesActive = $derived(
		Object.values(ranges).some((r) => r.min !== '' || r.max !== '')
	);
	const flagsActive = $derived(Object.values(flags).some((v) => v !== ''));

	// ---- upload ----
	let dragOver = $state(false);
	let uploading = $state(false);
	let uploadError = $state<string | null>(null);
	let uploadOk = $state<string | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);

	async function uploadFiles(files: FileList | null) {
		if (!files || files.length === 0) return;
		uploading = true;
		uploadError = null;
		uploadOk = null;
		let imported = 0;
		try {
			for (const file of Array.from(files)) {
				const fd = new FormData();
				fd.append('file', file);
				const res = await fetch('/api/activities/upload', { method: 'POST', body: fd });
				if (!res.ok) {
					uploadError = `${file.name}: ${(await res.text()).trim() || res.statusText}`;
					break;
				}
				imported++;
			}
			if (imported > 0) {
				uploadOk = `Imported ${imported} file${imported > 1 ? 's' : ''}.`;
				pageNum = 1;
				await loadFeed(1);
				await invalidateAll();
			}
		} catch (e) {
			uploadError = (e as Error).message;
		} finally {
			uploading = false;
		}
	}

	function onDrop(e: DragEvent) {
		e.preventDefault();
		dragOver = false;
		uploadFiles(e.dataTransfer?.files ?? null);
	}

	const hasFilter = $derived(
		typeFilter !== '' ||
			disciplineFilter !== '' ||
			fromDate !== '' ||
			toDate !== '' ||
			flagsActive ||
			rangesActive
	);
</script>

<section class="space-y-6">
	<header class="flex items-center justify-between gap-4 max-md:flex-wrap">
		<div>
			<h1 class="text-3xl font-semibold tracking-tight max-md:text-2xl">{m.activities_title()}</h1>
			{#if feed}
				<p class="mt-1 text-sm text-zinc-400">
					{#if hasFilter}
						<span class="font-medium text-zinc-200">{feed.matched}</span> of {feed.total} activities
					{:else}
						<span class="font-medium text-zinc-200">{feed.total}</span> activities
					{/if}
				</p>
			{/if}
		</div>
		<div class="flex items-center gap-2">
			<a
				href="/activities/new"
				class="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 hover:border-accent-500 hover:text-accent-300"
			>
				+ New activity
			</a>
			<button
				type="button"
				disabled={uploading}
				onclick={() => fileInput?.click()}
				class="rounded border border-accent-500 bg-accent-500/20 px-3 py-1.5 text-xs text-accent-300 hover:bg-accent-500/30 disabled:opacity-50"
			>
				{uploading ? 'Uploading…' : 'Upload files'}
			</button>
		</div>
	</header>

	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		role="button"
		tabindex="0"
		ondragover={(e) => {
			e.preventDefault();
			dragOver = true;
		}}
		ondragleave={() => (dragOver = false)}
		ondrop={onDrop}
		onclick={() => fileInput?.click()}
		onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && fileInput?.click()}
		class="cursor-pointer rounded-lg border-2 border-dashed px-5 py-6 text-center text-sm transition-colors {dragOver
			? 'border-accent-500 bg-accent-500/10 text-accent-200'
			: 'border-zinc-700 bg-zinc-900/30 text-zinc-400 hover:border-zinc-600'}"
	>
		Drag &amp; drop <code>.gpx</code>, <code>.tcx</code> or <code>.fit</code> files here (multiple
		supported), or click to browse.
		<input
			bind:this={fileInput}
			type="file"
			accept=".gpx,.tcx,.fit"
			multiple
			class="hidden"
			onchange={(e) => uploadFiles(e.currentTarget.files)}
		/>
	</div>

	{#if uploadError}
		<div class="rounded border border-red-700/50 bg-red-950/30 px-3 py-2 text-xs text-red-300">
			{uploadError}
		</div>
	{/if}
	{#if uploadOk}
		<div class="rounded border border-emerald-700/50 bg-emerald-950/30 px-3 py-2 text-xs text-emerald-300">
			{uploadOk}
		</div>
	{/if}

	<!-- Filter + sort bar — only renders facet chips for values the user has. -->
	{#if feed}
		<div class="space-y-3 rounded-lg border border-zinc-800 bg-zinc-900/40 p-4">
			{#if showSports}
				<div class="flex flex-wrap items-center gap-2">
					<span class="mr-1 text-xs font-medium uppercase tracking-wide text-zinc-500">Sport</span>
					<button
						type="button"
						onclick={() => {
							typeFilter = '';
							disciplineFilter = '';
						}}
						class="rounded-full px-3 py-1 text-xs {typeFilter === ''
							? 'bg-accent-500 text-zinc-950'
							: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
					>
						All
					</button>
					{#each feed.facets.types as f (f.value)}
						<button
							type="button"
							onclick={() => {
								typeFilter = typeFilter === f.value ? '' : f.value;
								disciplineFilter = '';
							}}
							class="rounded-full px-3 py-1 text-xs {typeFilter === f.value
								? 'bg-accent-500 text-zinc-950'
								: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
						>
							{humanize(f.value)} <span class="opacity-60">{f.count}</span>
						</button>
					{/each}
				</div>
			{/if}

			{#if showDisciplines}
				<div class="flex flex-wrap items-center gap-2">
					<span class="mr-1 text-xs font-medium uppercase tracking-wide text-zinc-500">Discipline</span>
					<button
						type="button"
						onclick={() => (disciplineFilter = '')}
						class="rounded-full px-3 py-1 text-xs {disciplineFilter === ''
							? 'bg-accent-500 text-zinc-950'
							: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
					>
						All
					</button>
					{#each feed.facets.disciplines as f (f.value)}
						<button
							type="button"
							onclick={() => (disciplineFilter = disciplineFilter === f.value ? '' : f.value)}
							class="rounded-full px-3 py-1 text-xs {disciplineFilter === f.value
								? 'bg-accent-500 text-zinc-950'
								: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
						>
							{humanize(f.value)} <span class="opacity-60">{f.count}</span>
						</button>
					{/each}
				</div>
			{/if}

			<!-- Date range: year selector + quick presets + custom bounds. -->
			<div class="flex flex-wrap items-center gap-2">
				<span class="mr-1 text-xs font-medium uppercase tracking-wide text-zinc-500">Date</span>
				{#if (feed.facets.years ?? []).length > 0}
					<select
						bind:value={yearFilter}
						onchange={applyYear}
						aria-label="Year"
						class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs focus:border-accent-400 focus:outline-none {yearFilter
							? 'border-accent-500 text-accent-300'
							: 'text-zinc-300'}"
					>
						<option value="">All years</option>
						{#each feed.facets.years ?? [] as y (y.value)}
							<option value={y.value}>{y.value} ({y.count})</option>
						{/each}
					</select>
				{/if}
				{#each DATE_PRESETS as p (p.days)}
					<button
						type="button"
						onclick={() => applyPreset(p.days)}
						class="rounded-full px-3 py-1 text-xs {activePreset === p.days
							? 'bg-accent-500 text-zinc-950'
							: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
					>
						{p.label}
					</button>
				{/each}
				<input
					type="date"
					bind:value={fromDate}
					onchange={() => {
						activePreset = null;
						yearFilter = '';
					}}
					aria-label="From date"
					class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300 focus:border-accent-400 focus:outline-none [color-scheme:dark]"
				/>
				<span class="text-xs text-zinc-600">–</span>
				<input
					type="date"
					bind:value={toDate}
					onchange={() => {
						activePreset = null;
						yearFilter = '';
					}}
					aria-label="To date"
					class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300 focus:border-accent-400 focus:outline-none [color-scheme:dark]"
				/>
			</div>

			<!-- Classification flags — tri-state chips: any → only → exclude. -->
			<div class="flex flex-wrap items-center gap-2">
				<span class="mr-1 text-xs font-medium uppercase tracking-wide text-zinc-500">Flags</span>
				{#each FLAGS as f (f.key)}
					<button
						type="button"
						onclick={() => cycleFlag(f.key)}
						title={flags[f.key] === ''
							? `${f.label}: any`
							: flags[f.key] === 'true'
								? `Only ${f.label.toLowerCase()}`
								: `No ${f.label.toLowerCase()}`}
						class="rounded-full px-3 py-1 text-xs {flags[f.key] === 'true'
							? 'bg-accent-500 text-zinc-950'
							: flags[f.key] === 'false'
								? 'bg-red-900/60 text-red-200 line-through'
								: 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700'}"
					>
						{f.label}
					</button>
				{/each}
				<button
					type="button"
					onclick={() => (moreFilters = !moreFilters)}
					class="ml-auto text-xs {rangesActive
						? 'text-accent-300'
						: 'text-zinc-500 hover:text-zinc-300'}"
				>
					{moreFilters ? 'Hide ranges ▴' : `Ranges${rangesActive ? ' •' : ''} ▾`}
				</button>
			</div>

			<!-- Numeric ranges (display units → SI in the query). -->
			{#if moreFilters}
				<div class="grid gap-x-6 gap-y-2 border-t border-zinc-800 pt-3 sm:grid-cols-2 lg:grid-cols-3">
					{#each RANGES as r (r.key)}
						<div class="flex items-center gap-2">
							<span class="w-28 shrink-0 text-xs text-zinc-500">{r.label}</span>
							<input
								type="number"
								min="0"
								placeholder="min"
								bind:value={ranges[r.key].min}
								aria-label={`${r.label} minimum (${r.unit})`}
								class="w-20 rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300 focus:border-accent-400 focus:outline-none"
							/>
							<span class="text-xs text-zinc-600">–</span>
							<input
								type="number"
								min="0"
								placeholder="max"
								bind:value={ranges[r.key].max}
								aria-label={`${r.label} maximum (${r.unit})`}
								class="w-20 rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300 focus:border-accent-400 focus:outline-none"
							/>
							<span class="text-xs text-zinc-600">{r.unit}</span>
						</div>
					{/each}
				</div>
			{/if}

			<div class="flex items-center gap-2 border-t border-zinc-800 pt-3">
				<label for="sort" class="text-xs font-medium uppercase tracking-wide text-zinc-500">Sort</label>
				<select
					id="sort"
					bind:value={sort}
					class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-xs text-zinc-300 focus:border-accent-400 focus:outline-none"
				>
					{#each SORTS as s (s.value)}
						<option value={s.value}>{s.label}</option>
					{/each}
				</select>
				{#if hasFilter}
					<button type="button" onclick={clearFilters} class="text-xs text-zinc-500 hover:text-zinc-300">
						Clear filters
					</button>
				{/if}
			</div>
		</div>
	{/if}

	{#if items.length === 0 && !loading}
		<div class="rounded-lg border border-zinc-800 bg-zinc-900/40 p-8 text-center text-sm text-zinc-400">
			{hasFilter ? 'No activities match this filter.' : m.activities_empty()}
		</div>
	{:else}
		<ul class="space-y-3 transition-opacity duration-200" class:opacity-50={loading}>
			{#each items as activity (activity.id)}
				<li>
					<a
						href={`/activities/${activity.id}`}
						class="flex items-center gap-5 rounded-lg border border-zinc-800 bg-zinc-900/40 px-5 py-4 transition-colors hover:border-zinc-700 hover:bg-zinc-900/80"
					>
						<div
							class="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-zinc-800 text-zinc-300"
							title={activity.discipline || activity.type}
						>
							<SportIcon type={activity.discipline || activity.type} size={24} />
						</div>
						<div class="min-w-0 flex-1">
							<div class="truncate font-medium">
								{activity.title || `${humanize(activity.discipline || activity.type)} Activity`}
							</div>
							<div class="mt-0.5 text-xs text-zinc-500">
								{formatRelativeDate(activity.start_time)}{activity.start_place
									? ` · ${humanize(activity.discipline || activity.type)} from ${activity.start_place}`
									: ` · ${activity.timezone}`}
							</div>
						</div>
						<img
							src={`/api/activities/${activity.id}/map.png`}
							alt=""
							loading="lazy"
							class="hidden h-8 w-24 shrink-0 rounded border border-zinc-800 object-cover lg:block"
							onerror={(e) => ((e.currentTarget as HTMLImageElement).style.display = 'none')}
						/>
						<dl class="hidden gap-6 text-right text-xs text-zinc-400 md:flex">
							<div>
								<dt class="text-zinc-500">{m.col_distance()}</dt>
								<dd class="mt-0.5 text-sm tabular-nums text-zinc-100">
									{formatDistance(activity.distance_m)}
								</dd>
							</div>
							<div>
								<dt class="text-zinc-500">{m.col_duration()}</dt>
								<dd class="mt-0.5 text-sm tabular-nums text-zinc-100">
									{formatDuration(activity.elapsed_duration_s)}
								</dd>
							</div>
							<div>
								<dt class="text-zinc-500">{m.col_elevation()}</dt>
								<dd class="mt-0.5 text-sm tabular-nums text-zinc-100">
									{formatElevation(activity.elevation_gain_m)}
								</dd>
							</div>
							<div>
								<dt class="text-zinc-500">TSS</dt>
								<dd class="mt-0.5 text-sm tabular-nums text-zinc-100">
									{activity.tss != null ? activity.tss.toFixed(0) : m.placeholder_dash()}
								</dd>
							</div>
						</dl>
					</a>
				</li>
			{/each}
		</ul>

		{#if feed && totalPages > 1}
			<nav class="mt-6 flex items-center justify-center gap-1" aria-label="Pagination">
				<button
					type="button"
					onclick={() => goToPage(pageNum - 1)}
					disabled={pageNum === 1}
					class="rounded px-3 py-1.5 text-sm text-zinc-300 hover:text-accent-300 disabled:opacity-40 disabled:hover:text-zinc-300"
				>
					‹ Prev
				</button>
				{#each pageList as p, i (i)}
					{#if p === '…'}
						<span class="px-2 text-sm text-zinc-600">…</span>
					{:else}
						<button
							type="button"
							onclick={() => goToPage(p)}
							aria-current={p === pageNum ? 'page' : undefined}
							class="min-w-9 rounded px-2.5 py-1.5 text-sm tabular-nums {p === pageNum
								? 'bg-accent-500 font-medium text-zinc-950'
								: 'text-zinc-300 hover:bg-zinc-800'}"
						>
							{p}
						</button>
					{/if}
				{/each}
				<button
					type="button"
					onclick={() => goToPage(pageNum + 1)}
					disabled={pageNum === totalPages}
					class="rounded px-3 py-1.5 text-sm text-zinc-300 hover:text-accent-300 disabled:opacity-40 disabled:hover:text-zinc-300"
				>
					Next ›
				</button>
			</nav>
			<p class="mt-2 text-center text-xs text-zinc-500">
				{(pageNum - 1) * PAGE_SIZE + 1}–{Math.min(pageNum * PAGE_SIZE, feed.matched)} of {feed.matched}
			</p>
		{/if}
	{/if}
</section>
