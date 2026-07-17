<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import uPlot, { type AlignedData, type Options } from 'uplot';
	import 'uplot/dist/uPlot.min.css';

	interface Series {
		label: string;
		values: (number | null)[];
		color: string;
		unit?: string;
		fill?: boolean; // render as a filled area (e.g. elevation as a mountain profile)
	}

	let {
		offsets,
		series,
		height = 300,
		syncKey,
		hoverT = null,
		onHover
	}: {
		offsets: number[];
		series: Series[];
		height?: number;
		syncKey?: string;
		// hoverT: externally-driven hover position (seconds offset) — shows the
		// cursor at that x without emitting. null clears it.
		hoverT?: number | null;
		// onHover: fires with the hovered time (seconds offset) as the user moves
		// over the chart, or null on leave. Lets a parent sync a map marker.
		onHover?: (t: number | null) => void;
	} = $props();

	let container: HTMLDivElement;
	let plot: uPlot | undefined;
	let zoomed = $state(false);
	// Guards the setCursor hook from re-emitting when WE move the cursor
	// programmatically from an external hoverT.
	let applyingExternal = false;

	// Full x-extent of the data; used to detect zoom state and to reset.
	function fullRange(): [number, number] {
		return [offsets[0], offsets[offsets.length - 1]];
	}

	function resetZoom() {
		if (!plot) return;
		const [min, max] = fullRange();
		plot.setScale('x', { min, max });
	}

	function build() {
		if (!container || offsets.length === 0 || series.length === 0) return;

		// Filled series (e.g. elevation) render FIRST so they sit behind the
		// metric lines like a background mountain profile.
		const ordered = [...series].sort((a, b) => Number(b.fill ?? false) - Number(a.fill ?? false));

		const data: AlignedData = [offsets, ...ordered.map((s) => s.values)] as AlignedData;

		// Each series gets its OWN auto-ranged y-scale so channels with very
		// different magnitudes (e.g. elevation 0–2000 m vs HR 100–180 bpm) are
		// each visible across the full height instead of one flattening the
		// rest. A single shared y-axis would be meaningless across mixed units,
		// so we render only the x-axis; per-series values appear in the legend.
		const scales: Options['scales'] = { x: { time: false } };
		ordered.forEach((_, i) => {
			scales![`y${i}`] = {};
		});

		const opts: Options = {
			width: container.clientWidth || 800,
			height,
			cursor: {
				drag: { x: true, y: false },
				// When a syncKey is given, join a uPlot sync group so the hover
				// cursor (and drag-zoom selection) align across stacked charts.
				...(syncKey ? { sync: { key: syncKey, setSeries: true } } : {})
			},
			scales,
			axes: [
				{
					stroke: '#71717a',
					grid: { stroke: '#27272a' },
					values: (_u, splits) => splits.map((s) => formatSeconds(s))
				}
			],
			series: [
				{ label: 'Zeit' },
				...ordered.map((s, i) => ({
					label: s.unit ? `${s.label} (${s.unit})` : s.label,
					stroke: s.color,
					width: s.fill ? 1 : 1.5,
					// hex + alpha → translucent area fill for the mountain look.
					fill: s.fill ? s.color + '2e' : undefined,
					scale: `y${i}`,
					points: { show: false }
				}))
			],
			hooks: {
				// Toggle the reset button whenever the x-scale changes (drag-zoom
				// narrows it; double-click / reset restores the full extent).
				setScale: [
					(u, key) => {
						if (key !== 'x') return;
						const { min, max } = u.scales.x;
						const [full0, full1] = fullRange();
						const span = full1 - full0 || 1;
						const eps = span * 1e-6;
						zoomed =
							min != null && max != null && (min > full0 + eps || max < full1 - eps);
					}
				],
				// Emit the hovered time so a parent can sync a map marker. Skipped
				// when the cursor was moved programmatically (external hoverT).
				setCursor: [
					(u) => {
						if (applyingExternal || !onHover) return;
						const idx = u.cursor.idx;
						onHover(idx == null ? null : offsets[idx]);
					}
				]
			}
		};

		plot?.destroy();
		plot = new uPlot(opts, data, container);
	}

	function formatSeconds(s: number): string {
		const total = Math.round(s);
		const h = Math.floor(total / 3600);
		const m = Math.floor((total % 3600) / 60);
		const sec = total % 60;
		if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${sec.toString().padStart(2, '0')}`;
		return `${m}:${sec.toString().padStart(2, '0')}`;
	}

	onMount(() => {
		build();
		const ro = new ResizeObserver(() => {
			if (plot && container) {
				plot.setSize({ width: container.clientWidth, height });
			}
		});
		ro.observe(container);
		return () => ro.disconnect();
	});

	onDestroy(() => {
		plot?.destroy();
	});

	$effect(() => {
		// Rebuild when offsets or series identity changes.
		offsets;
		series;
		build();
	});

	$effect(() => {
		// Apply an externally-driven hover position (e.g. from the map) by moving
		// the uPlot cursor to that x. Setting left off-canvas hides it.
		if (!plot) return;
		applyingExternal = true;
		if (hoverT == null) {
			plot.setCursor({ left: -10, top: -10 });
		} else {
			const left = plot.valToPos(hoverT, 'x');
			plot.setCursor({ left, top: height / 2 });
		}
		applyingExternal = false;
	});
</script>

<div class="relative w-full">
	{#if zoomed}
		<button
			type="button"
			onclick={resetZoom}
			class="absolute right-2 top-2 z-10 rounded border border-zinc-700 bg-zinc-900/90 px-2 py-1 text-xs text-zinc-300 shadow hover:border-accent-500 hover:text-accent-300 max-md:py-2"
		>
			Reset zoom
		</button>
	{/if}
	<div bind:this={container} class="w-full"></div>
</div>
