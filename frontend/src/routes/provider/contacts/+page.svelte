<script lang="ts">
	import ContactsEditor from '#lib/provider/ContactsEditor.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { provider, setListing } from '#lib/provider/state.svelte.ts';
	import { ownTarget } from '#lib/provider/types.ts';
</script>

<h1>{t('app.provider.tab.contacts')}</h1>
<p class="lead">{t('app.provider.lead.contacts')}</p>
{#if provider.listing}
	{#if provider.listing.status === 'archived'}<p class="notice">{t('errors.listing_archived')}</p>{/if}
	<ContactsEditor target={ownTarget} listing={provider.listing} onsaved={setListing} />
{:else}
	<p><a href="/provider">{t('app.provider.start_title')}</a></p>
{/if}
