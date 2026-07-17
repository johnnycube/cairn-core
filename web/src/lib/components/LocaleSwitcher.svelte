<script lang="ts">
	import { getLocale, setLocale, locales, type Locale } from '$lib/paraglide/runtime';
	import { m } from '$lib/paraglide/messages';

	const current = $derived(getLocale());

	const labels: Record<Locale, string> = {
		de: m.locale_de(),
		en: m.locale_en()
	};

	// Static SPA: no /set-locale server route. Paraglide's setLocale writes
	// the PARAGLIDE_LOCALE cookie (the cookie strategy is enabled) and
	// reloads so messages re-resolve in the chosen locale.
	function onSelect(value: string) {
		if (locales.includes(value as Locale)) {
			setLocale(value as Locale);
		}
	}
</script>

<div class="flex items-center gap-1 text-xs">
	<label for="cairn-locale-select" class="sr-only">{m.language_switcher_label()}</label>
	<select
		id="cairn-locale-select"
		value={current}
		onchange={(e) => onSelect(e.currentTarget.value)}
		class="rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-zinc-300 focus:border-accent-400 focus:outline-none"
	>
		{#each locales as locale (locale)}
			<option value={locale}>{labels[locale]}</option>
		{/each}
	</select>
</div>
