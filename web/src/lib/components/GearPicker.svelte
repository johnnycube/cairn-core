<script lang="ts">
	import { m } from '#lib/paraglide/messages.js';
	import { gearLabel, pickerOptions, type GearView } from '#lib/gear.js';

	// Gear select for an activity of `type`: matching kind first, retired gear
	// only when it is the current choice. "" = no gear.
	let {
		gear,
		type,
		value = $bindable(''),
		id = 'gear',
		disabled = false
	}: { gear: GearView[]; type: string; value?: string; id?: string; disabled?: boolean } = $props();

	const options = $derived(pickerOptions(gear, type, value));
	const label = (g: GearView) => gearLabel(g) + (g.retired ? ` ${m.gear_retired_suffix()}` : '');
</script>

<select
	{id}
	bind:value
	{disabled}
	class="mt-1 w-full rounded border border-zinc-800 bg-zinc-900/60 px-3 py-2 text-sm disabled:opacity-50"
>
	<option value="">{m.gear_picker_none()}</option>
	{#each options.matching as g (g.id)}
		<option value={g.id}>{label(g)}</option>
	{/each}
	{#if options.others.length > 0}
		<option disabled>──────</option>
		{#each options.others as g (g.id)}
			<option value={g.id}>{label(g)}</option>
		{/each}
	{/if}
</select>
