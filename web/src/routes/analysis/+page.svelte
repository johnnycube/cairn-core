<script lang="ts">
	import { onMount } from 'svelte';

	type Analysis = {
		dates: string[];
		ctl: number[];
		atl: number[];
		tsb: number[];
		tss: number[];
		current: { ctl: number; atl: number; tsb: number };
	};

	let { data } = $props();

	let days = $state(180);
	let a = $state<Analysis | null>(null);
	let loading = $state(true);

	async function load() {
		loading = true;
		try {
			const res = await fetch(`/api/analysis?days=${days}`);
			if (res.ok) a = await res.json();
		} catch {
			/* ignore */
		}
		loading = false;
	}

	onMount(load);

	function setDays(d: number) {
		days = d;
		load();
	}

	// ---- inline SVG chart geometry --------------------------------------
	const W = 880;
	const H = 280;
	const PAD = { t: 16, r: 16, b: 28, l: 40 };
	const plotW = W - PAD.l - PAD.r;
	const plotH = H - PAD.t - PAD.b;

	type Series = { key: 'ctl' | 'atl' | 'tsb'; label: string; color: string };
	const series: Series[] = [
		{ key: 'ctl', label: 'Fitness (CTL)', color: '#38bdf8' },
		{ key: 'atl', label: 'Fatigue (ATL)', color: '#f472b6' },
		{ key: 'tsb', label: 'Form (TSB)', color: '#a3e635' }
	];

	const bounds = $derived.by(() => {
		if (!a || a.dates.length === 0) return { min: 0, max: 1 };
		let min = Infinity;
		let max = -Infinity;
		for (const s of series) {
			for (const v of a[s.key]) {
				if (v < min) min = v;
				if (v > max) max = v;
			}
		}
		if (!isFinite(min)) return { min: 0, max: 1 };
		// pad a touch so lines aren't glued to edges
		const span = max - min || 1;
		return { min: min - span * 0.08, max: max + span * 0.08 };
	});

	function x(i: number, n: number): number {
		if (n <= 1) return PAD.l;
		return PAD.l + (i / (n - 1)) * plotW;
	}
	function y(v: number): number {
		const { min, max } = bounds;
		return PAD.t + plotH - ((v - min) / (max - min || 1)) * plotH;
	}
	function path(vals: number[]): string {
		const n = vals.length;
		return vals.map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i, n).toFixed(1)},${y(v).toFixed(1)}`).join(' ');
	}

	// y=0 gridline (form crosses zero — meaningful for TSB)
	const zeroY = $derived(bounds.min <= 0 && bounds.max >= 0 ? y(0) : null);

	function fmt(v: number): string {
		return v >= 0 ? `+${v.toFixed(1)}` : v.toFixed(1);
	}

	// hover crosshair
	let hover = $state<number | null>(null);
	function onMove(e: MouseEvent, n: number) {
		const svg = e.currentTarget as SVGSVGElement;
		const rect = svg.getBoundingClientRect();
		const px = ((e.clientX - rect.left) / rect.width) * W;
		const i = Math.round(((px - PAD.l) / plotW) * (n - 1));
		hover = Math.max(0, Math.min(n - 1, i));
	}

	const ranges = [
		{ label: '3M', d: 90 },
		{ label: '6M', d: 180 },
		{ label: '1Y', d: 365 },
		{ label: 'All', d: 3650 }
	];
</script>

<div class="space-y-6">
	<header>
		<h1 class="text-3xl font-semibold tracking-tight max-md:text-2xl">Analysis</h1>
		<p class="mt-1 text-sm text-zinc-400">
			Track real fitness progress over time. Fitness (CTL) is your chronic training load,
			Fatigue (ATL) the acute load, and Form (TSB = CTL − ATL) tells you how fresh you are.
		</p>
	</header>

	{#if !data.user}
		<a
			href="/login"
			class="inline-block rounded border border-accent-500 bg-accent-500/20 px-4 py-2 text-sm text-accent-300 hover:bg-accent-500/30"
		>
			Sign in
		</a>
	{:else}
		<!-- current snapshot -->
		{#if a}
			<div class="grid grid-cols-3 gap-4 max-md:grid-cols-1">
				{#each series as s (s.key)}
					<div class="rounded-xl border border-zinc-800 bg-zinc-900/40 p-5">
						<div class="flex items-center gap-2 text-xs uppercase tracking-wide text-zinc-500">
							<span class="inline-block h-2.5 w-2.5 rounded-full" style:background={s.color}></span>
							{s.label}
						</div>
						<div class="mt-1 text-2xl font-semibold tabular-nums text-zinc-100">
							{s.key === 'tsb' ? fmt(a.current[s.key]) : a.current[s.key].toFixed(1)}
						</div>
					</div>
				{/each}
			</div>
		{/if}

		<!-- range picker -->
		<div class="flex gap-1">
			{#each ranges as r (r.d)}
				<button
					type="button"
					onclick={() => setDays(r.d)}
					class="rounded px-3 py-1 text-xs transition-colors"
					class:bg-zinc-800={days === r.d}
					class:text-zinc-100={days === r.d}
					class:text-zinc-400={days !== r.d}
					class:hover:text-zinc-100={days !== r.d}
				>
					{r.label}
				</button>
			{/each}
		</div>

		<!-- chart -->
		<div class="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
			{#if loading}
				<div class="flex h-[280px] items-center justify-center text-sm text-zinc-500">Loading…</div>
			{:else if !a || a.dates.length === 0}
				<div class="flex h-[280px] items-center justify-center text-sm text-zinc-500">
					No training-load data yet. Import some activities first.
				</div>
			{:else}
				{@const n = a.dates.length}
				<svg
					viewBox={`0 0 ${W} ${H}`}
					class="w-full"
					role="img"
					aria-label="Training load trend"
					onmousemove={(e) => onMove(e, n)}
					onmouseleave={() => (hover = null)}
				>
					<!-- y axis gridlines -->
					{#each [0, 0.25, 0.5, 0.75, 1] as t (t)}
						{@const gy = PAD.t + plotH * t}
						{@const val = bounds.max - (bounds.max - bounds.min) * t}
						<line x1={PAD.l} y1={gy} x2={W - PAD.r} y2={gy} stroke="#27272a" stroke-width="1" />
						<text x={PAD.l - 6} y={gy + 3} text-anchor="end" class="fill-zinc-600 text-[10px]">
							{val.toFixed(0)}
						</text>
					{/each}

					<!-- zero line for TSB -->
					{#if zeroY !== null}
						<line
							x1={PAD.l}
							y1={zeroY}
							x2={W - PAD.r}
							y2={zeroY}
							stroke="#3f3f46"
							stroke-width="1"
							stroke-dasharray="3 3"
						/>
					{/if}

					<!-- series -->
					{#each series as s (s.key)}
						<path d={path(a[s.key])} fill="none" stroke={s.color} stroke-width="1.8" />
					{/each}

					<!-- hover crosshair -->
					{#if hover !== null}
						{@const hx = x(hover, n)}
						<line x1={hx} y1={PAD.t} x2={hx} y2={PAD.t + plotH} stroke="#52525b" stroke-width="1" />
						{#each series as s (s.key)}
							<circle cx={hx} cy={y(a[s.key][hover])} r="3" fill={s.color} />
						{/each}
					{/if}
				</svg>

				<!-- legend / hover readout -->
				<div class="mt-2 flex flex-wrap items-center justify-between gap-3 text-xs">
					<div class="flex flex-wrap gap-4">
						{#each series as s (s.key)}
							<span class="flex items-center gap-1.5 text-zinc-400">
								<span class="inline-block h-2 w-2 rounded-full" style:background={s.color}></span>
								{s.label}
								{#if hover !== null}
									<span class="tabular-nums text-zinc-200">
										{s.key === 'tsb' ? fmt(a[s.key][hover]) : a[s.key][hover].toFixed(1)}
									</span>
								{/if}
							</span>
						{/each}
					</div>
					{#if hover !== null}
						<span class="tabular-nums text-zinc-500">{a.dates[hover]}</span>
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</div>
