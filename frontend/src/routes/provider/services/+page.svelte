<script lang="ts">
	import ServicesEditor from '#lib/provider/ServicesEditor.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { provider, setListing } from '#lib/provider/state.svelte.ts';
	import { ownTarget } from '#lib/provider/types.ts';
</script>

<h1>{t('app.provider.tab.services')}</h1>
<p class="lead">{t('app.provider.lead.services')}</p>
{#if provider.listing}
	{#if provider.listing.status === 'archived'}<p class="notice">{t('errors.listing_archived')}</p>{/if}
	<ServicesEditor target={ownTarget} listing={provider.listing} onsaved={setListing} />
{:else}
	<p><a href="/provider">{t('app.provider.start_title')}</a></p>
{/if}
