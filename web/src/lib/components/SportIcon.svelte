<script lang="ts">
	// Vector sport icons. `type` is the lowercase activity type (ride, run,
	// swim, hike, walk, row, ski, workout, + the extended sport set); unknown →
	// a generic activity glyph. Inherits color via currentColor, sizes via `size`.
	let { type = '', size = 20 }: { type?: string; size?: number } = $props();

	// Map a discipline-ish value down to its base sport for icon selection.
	const base = $derived(
		(() => {
			const t = (type || '').toLowerCase();
			if (t.startsWith('ride')) return 'ride';
			if (t.startsWith('run')) return 'run';
			if (t.startsWith('swim')) return 'swim';
			if (t.startsWith('hike')) return 'hike';
			if (t.startsWith('walk')) return 'walk';
			if (t.startsWith('row')) return 'row';
			if (t.startsWith('ski')) return 'ski';
			if (t.startsWith('workout')) return 'workout';
			if (t.startsWith('snowboard')) return 'snowboard';
			if (t.startsWith('skate')) return 'skate';
			if (t.startsWith('kayak')) return 'kayak';
			if (t.startsWith('sup')) return 'sup';
			if (t.startsWith('surf')) return 'surf';
			if (t.startsWith('golf')) return 'golf';
			if (t.startsWith('climb')) return 'climb';
			if (t.startsWith('tennis')) return 'tennis';
			if (t.startsWith('elliptical')) return 'elliptical';
			if (t.startsWith('wheelchair')) return 'wheelchair';
			return t;
		})()
	);
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="1.7"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
>
	{#if base === 'ride'}
		<!-- bicycle -->
		<circle cx="5.5" cy="17.5" r="3.5" />
		<circle cx="18.5" cy="17.5" r="3.5" />
		<circle cx="15" cy="5" r="1" />
		<path d="M12 17.5 9 9l3-2 3 3h2.5M9 9h6.5" />
	{:else if base === 'run'}
		<!-- running figure -->
		<circle cx="14" cy="4.5" r="1.6" />
		<path d="M13 8.5 9 11l2 3-2 5M13 8.5l3 2 3-1M11 14l4 1 1 4" />
	{:else if base === 'swim'}
		<!-- swimmer + waves -->
		<circle cx="17" cy="7" r="1.5" />
		<path d="M3 10c2 0 3-1.5 5-1.5L14.5 11l2-1.5" />
		<path d="M2 15c1.2 1 2.4 1 3.5 0 1.2-1 2.3-1 3.5 0 1.2 1 2.3 1 3.5 0 1.2-1 2.3-1 3.5 0 .6.5 1.2.6 2 .3" />
		<path d="M2 19c1.2 1 2.4 1 3.5 0 1.2-1 2.3-1 3.5 0 1.2 1 2.3 1 3.5 0 1.2-1 2.3-1 3.5 0 .6.5 1.2.6 2 .3" />
	{:else if base === 'hike'}
		<!-- mountains -->
		<path d="m8 4 4.5 9 2.5-3 5 11H4z" />
		<circle cx="8" cy="4" r="0" />
	{:else if base === 'walk'}
		<!-- walking figure -->
		<circle cx="13" cy="4.5" r="1.6" />
		<path d="M12 8 10 13l1 7M12 8l3 2.5.5 3M10 13l-1.5 7M12 8l1.5 3" />
	{:else if base === 'row'}
		<!-- rowing / boat + oar -->
		<path d="M3 17c2.5 2.5 15.5 2.5 18 0" />
		<path d="m5 14 7-2 7 2M9 7l6 6" />
		<circle cx="8" cy="6" r="1.4" />
	{:else if base === 'ski'}
		<!-- skis + poles -->
		<path d="M4 19 16 5M7 19 19 5" />
		<path d="M3.5 18.5c.8.8 1.8.8 2.6 0M17.4 18.5c.8.8 1.8.8 2.6 0" />
		<path d="M9 12l2 2" />
	{:else if base === 'workout'}
		<!-- dumbbell -->
		<path d="M4 9v6M7 7v10M17 7v10M20 9v6M7 12h10" />
	{:else if base === 'snowboard'}
		<!-- snowboard + rider -->
		<circle cx="15" cy="4.5" r="1.5" />
		<path d="M13 8l-2 4 3 2-1 5M13 8l3 1 2-1" />
		<path d="M5 18 19 12" />
		<path d="M6.5 16.6l1.6 2.8M16.1 11.2l1.6 2.8" />
	{:else if base === 'skate'}
		<!-- inline skate -->
		<path d="M6 4v8h9l2 3v2H6" />
		<circle cx="7.5" cy="20" r="1.3" />
		<circle cx="11.5" cy="20" r="1.3" />
		<circle cx="15.5" cy="20" r="1.3" />
	{:else if base === 'kayak'}
		<!-- kayak + double paddle -->
		<path d="M3 12c4 3 14 3 18 0" />
		<path d="M4 8l16 8M6.5 7l-2 2M19.5 17l-2 2" />
	{:else if base === 'sup'}
		<!-- stand-up paddler -->
		<circle cx="12" cy="4" r="1.5" />
		<path d="M12 6v7M12 9l4-3M8 13h8" />
		<path d="M3 18c2 1.5 4 1.5 6 0s4-1.5 6 0 4 1.5 6 0" />
	{:else if base === 'surf'}
		<!-- surfboard + wave -->
		<path d="M4 20c6-1 12-7 16-16" />
		<path d="M3 17c2 1.5 4 1.5 6 0" />
		<ellipse cx="9" cy="13" rx="2" ry="6" transform="rotate(45 9 13)" />
	{:else if base === 'golf'}
		<!-- flag + hole -->
		<path d="M8 3v15M8 3l8 2.5L8 8" />
		<path d="M4 20c1.5 1 8.5 1 10 0" />
	{:else if base === 'climb'}
		<!-- climber on wall -->
		<circle cx="13" cy="4.5" r="1.5" />
		<path d="M13 6 8 9l3 3-2 8M13 6l4 3M11 12l5 2" />
	{:else if base === 'tennis'}
		<!-- racket + ball -->
		<ellipse cx="9" cy="8" rx="5" ry="6" />
		<path d="M12.5 12.5 19 19" />
		<circle cx="18" cy="6" r="1.2" />
	{:else if base === 'elliptical'}
		<!-- elliptical trainer -->
		<ellipse cx="12" cy="15" rx="9" ry="4" />
		<path d="M5 14 12 5l4 2" />
		<circle cx="12" cy="5" r="0.6" />
	{:else if base === 'wheelchair'}
		<!-- wheelchair -->
		<circle cx="11" cy="16" r="5" />
		<circle cx="13" cy="4.5" r="1.5" />
		<path d="M13 7v5h4l2 5M13 9h3" />
	{:else}
		<!-- generic activity / pulse -->
		<path d="M3 12h4l2-6 4 12 2-6h6" />
	{/if}
</svg>
