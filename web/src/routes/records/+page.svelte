<script lang="ts">
	import { onMount } from 'svelte';
	import SportIcon from '$lib/components/SportIcon.svelte';
	import { formatSpeed, formatPower, formatHeartRate, formatDateOnly } from '$lib/format';

	// Cross-provider personal records (brief §11): the all-time best per
	// (activity type, metric, window) across every activity + provider.
	type Record = {
		activity_type: string;
		metric: string;
		window_kind: string;
		window_value: number;
		achieved_value: number;
		activity_id: string;
		timestamp: string;
	};

	let records = $state<Record[]>([]);
	let loaded = $state(false);
	let error = $state<string | null>(null);

	onMount(async () => {
		try {
			const res = await fetch('/api/records');
			if (!res.ok) throw new Error(await res.text());
			const b = await res.json();
			records = b.records ?? [];
		} catch (e) {
			error = (e as Error).message || 'failed to load';
		}
		loaded = true;
	});

	// Group by activity type, preserving a sensible type order.
	const TYPE_ORDER = ['ride', 'run', 'swim', 'walk', 'hike', 'workout'];
	let byType = $derived.by(() => {
		const m = new Map<string, Record[]>();
		for (const r of records) {
			if (!m.has(r.activity_type)) m.set(r.activity_type, []);
			m.get(r.activity_type)!.push(r);
		}
		// sort each group by metric then window.
		for (const list of m.values()) {
			list.sort((a, b) =>
				a.metric === b.metric ? a.window_value - b.window_value : a.metric.localeCompare(b.metric)
			);
		}
		return [...m.entries()].sort(
			(a, b) => typeRank(a[0]) - typeRank(b[0]) || a[0].localeCompare(b[0])
		);
	});
	function typeRank(t: string): number {
		const i = TYPE_ORDER.indexOf(t);
		return i === -1 ? 999 : i;
	}

	function windowLabel(kind: string, value: number): string {
		if (kind === 'distance') {
			if (value >= 1000) return value % 1000 === 0 ? `${value / 1000} km` : `${(value / 1000).toFixed(2)} km`;
			return `${value} m`;
		}
		// duration (seconds)
		if (value >= 3600) return value % 3600 === 0 ? `${value / 3600} h` : `${(value / 3600).toFixed(1)} h`;
		if (value >= 60) return value % 60 === 0 ? `${value / 60} min` : `${Math.round(value / 60)} min`;
		return `${value} s`;
	}

	function metricLabel(metric: string): string {
		switch (metric) {
			case 'pace': return 'Fastest';
			case 'speed': return 'Top speed';
			case 'power': return 'Best power';
			case 'heart_rate': return 'Peak HR';
			case 'vam': return 'Best VAM';
			default: return metric;
		}
	}

	function formatValue(metric: string, v: number): string {
		switch (metric) {
			case 'pace': {
				// seconds per km → mm:ss /km
				const s = Math.round(v);
				return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')} /km`;
			}
			case 'speed': return formatSpeed(v);
			case 'power': return formatPower(v);
			case 'heart_rate': return formatHeartRate(v);
			case 'vam': return `${Math.round(v)} m/h`;
			default: return String(Math.round(v));
		}
	}
</script>

<svelte:head><title>Records · Cairn</title></svelte:head>

<div class="mx-auto max-w-4xl px-4 py-6">
	<h1 class="mb-1 text-xl font-semibold text-zinc-100">Personal records</h1>
	<p class="mb-6 text-sm text-zinc-500">
		Your all-time bests across every activity and every connected provider — a unified
		view no single provider can produce.
	</p>

	{#if !loaded}
		<p class="text-sm text-zinc-600">Loading…</p>
	{:else if error}
		<p class="text-sm text-red-400">{error}</p>
	{:else if records.length === 0}
		<p class="text-sm text-zinc-500">
			No records yet — they appear once you've imported activities with power, pace, or
			heart-rate data.
		</p>
	{:else}
		<div class="space-y-8">
			{#each byType as [type, list] (type)}
				<section>
					<h2 class="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wide text-zinc-400">
						<SportIcon type={type} size={16} />
						{type}
					</h2>
					<div class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
						{#each list as r (r.metric + r.window_kind + r.window_value)}
							<a
								href={`/activities/${r.activity_id}`}
								class="rounded-lg border border-zinc-800 bg-zinc-900/40 p-3 hover:border-accent-600"
							>
								<div class="text-xs text-zinc-500">
									{metricLabel(r.metric)} · {windowLabel(r.window_kind, r.window_value)}
								</div>
								<div class="mt-0.5 text-lg font-semibold text-zinc-100">
									{formatValue(r.metric, r.achieved_value)}
								</div>
								<div class="mt-0.5 text-xs text-zinc-600">{formatDateOnly(r.timestamp)}</div>
							</a>
						{/each}
					</div>
				</section>
			{/each}
		</div>
	{/if}
</div>
