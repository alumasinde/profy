<script lang="ts">
	import { t } from '#lib/i18n.svelte.ts';
	import type { Listing } from './types.ts';

	let { listing, base = '/provider' }: { listing: Listing; base?: string } = $props();
	// Where each checklist item is completed (owner area).
	const where: Record<string, string> = {
		display_name: '/profile', description: '/profile', experience: '/profile', terms: '/profile',
		services: '/services', base_area: '/areas', service_areas: '/areas', contacts: '/contacts',
		profile_photo: '/photos', portfolio: '/photos'
	};
</script>

<section class="card" aria-labelledby="cmp-h">
	<h2 id="cmp-h">{t('app.provider.completeness', { score: listing.completeness.score })}</h2>
	<progress max="100" value={listing.completeness.score} aria-label={t('app.provider.completeness', { score: listing.completeness.score })}></progress>
	<ul class="checklist">
		{#each listing.completeness.items as it (it.key)}
			<li class:done={it.done}>
				<span aria-hidden="true">{it.done ? '✓' : '○'}</span>
				<a href={base + where[it.key]}>{t('app.provider.item.' + it.key)}</a>
				{#if it.required && !it.done}<span class="badge status-draft">{t('app.provider.required')}</span>{/if}
				<span class="sr-only">{it.done ? t('app.provider.done') : t('app.provider.todo')}</span>
			</li>
		{/each}
	</ul>
</section>

<style>
	progress { width: 100%; height: 0.75rem; }
	.checklist { list-style: none; padding: 0; margin: 0.75rem 0 0; display: grid; gap: 0.35rem; }
	.checklist li { display: flex; gap: 0.5rem; align-items: center; }
	.checklist li.done a { color: var(--color-muted, #475569); }
</style>
