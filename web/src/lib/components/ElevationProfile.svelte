<script lang="ts">
	// A compact elevation-vs-distance profile (inline SVG, no chart lib). Used
	// above the segment-efforts table; `highlight` shades the distance range a
	// hovered segment covers.
	type Pt = { distM: number | null; eleM: number | null };

	let {
		track = [],
		highlight = null,
		height = 96
	}: {
		track: Pt[];
		// highlight: distance range (metres) to shade, e.g. a hovered segment.
		highlight?: { startDist: number; endDist: number } | null;
		height?: number;
	} = $props();

	const W = 1000; // viewBox width; SVG scales responsively to the container.

	// Build (x, y) points. x is cumulative distance when available, else sample
	// index; y is elevation. Only points with a finite elevation are used.
	const pts = $derived.by(() => {
		const out: { x: number; y: number }[] = [];
		let i = 0;
		for (const p of track) {
			if (p.eleM == null || !Number.isFinite(p.eleM)) {
				i++;
				continue;
			}
			const x = p.distM != null && Number.isFinite(p.distM) ? p.distM : i;
			out.push({ x, y: p.eleM });
			i++;
		}
		return out;
	});

	const bounds = $derived.by(() => {
		if (pts.length < 2) return null;
		let xMin = Infinity,
			xMax = -Infinity,
			yMin = Infinity,
			yMax = -Infinity;
		for (const p of pts) {
			if (p.x < xMin) xMin = p.x;
			if (p.x > xMax) xMax = p.x;
			if (p.y < yMin) yMin = p.y;
			if (p.y > yMax) yMax = p.y;
		}
		if (xMax - xMin < 1e-6 || yMax - yMin < 1e-6) return null;
		return { xMin, xMax, yMin, yMax };
	});

	function sx(x: number): number {
		if (!bounds) return 0;
		return ((x - bounds.xMin) / (bounds.xMax - bounds.xMin)) * W;
	}
	// Pad the y-range a touch so the line doesn't hug the edges; invert (higher
	// elevation = smaller SVG y).
	function sy(y: number): number {
		if (!bounds) return height;
		const pad = (bounds.yMax - bounds.yMin) * 0.08;
		const lo = bounds.yMin - pad;
		const hi = bounds.yMax + pad;
		return height - ((y - lo) / (hi - lo)) * height;
	}

	const linePath = $derived.by(() => {
		if (!bounds) return '';
		return pts.map((p, i) => `${i === 0 ? 'M' : 'L'}${sx(p.x).toFixed(1)},${sy(p.y).toFixed(1)}`).join(' ');
	});
	const areaPath = $derived(linePath ? `${linePath} L${W},${height} L0,${height} Z` : '');

	const hi = $derived.by(() => {
		if (!highlight || !bounds) return null;
		const a = Math.max(0, sx(highlight.startDist));
		const b = Math.min(W, sx(highlight.endDist));
		if (b <= a) return null;
		return { x: a, w: b - a };
	});
</script>

{#if bounds}
	<svg viewBox="0 0 {W} {height}" preserveAspectRatio="none" class="block h-24 w-full" role="img" aria-label="Elevation profile">
		<defs>
			<linearGradient id="ele-fill" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0%" stop-color="#a78bfa" stop-opacity="0.35" />
				<stop offset="100%" stop-color="#a78bfa" stop-opacity="0.04" />
			</linearGradient>
		</defs>
		{#if hi}
			<rect x={hi.x} y="0" width={hi.w} height={height} fill="#ec7a45" fill-opacity="0.22" />
		{/if}
		<path d={areaPath} fill="url(#ele-fill)" />
		<path d={linePath} fill="none" stroke="#a78bfa" stroke-width="1.5" vector-effect="non-scaling-stroke" />
	</svg>
{/if}
