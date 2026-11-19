<script lang="ts">
	import { page } from '$app/state';
	import FormError from '#lib/components/FormError.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { loadListing, provider } from '#lib/provider/state.svelte.ts';
	import { session } from '#lib/session.svelte.ts';

	let { children } = $props();
	$effect(() => {
		if (session.authenticated) loadListing();
	});
	const tabs = ['', '/profile', '/services', '/areas', '/contacts', '/photos', '/verification'];
	const here = (s: string) => (page.url.pathname === '/provider' + s ? 'page' : undefined);
	const bare = $derived(page.url.pathname.startsWith('/provider/claim'));
</script>

{#if bare}
	{@render children()}
{:else if !provider.loaded}
	<p class="muted" aria-live="polite">{t('app.loading')}</p>
{:else}
	<FormError error={provider.error} />
	{#if provider.listing}
		<nav class="subnav" aria-label={t('app.provider.nav_label')}>
			{#each tabs as s (s)}<a href={'/provider' + s} aria-current={here(s)}>{t('app.provider.tab.' + (s.slice(1) || 'overview'))}</a>{/each}
		</nav>
	{/if}
	{@render children()}
{/if}
