<script lang="ts">
	// Buttons for the draft → published → archived lifecycle. Display only: the API checks
	// <prefix>.publish / <prefix>.archive for every transition.
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	let { status, prefix, onchange }: { status: string; prefix: string; onchange: (next: string) => void } = $props();
	const canPublish = $derived(can(prefix + '.publish'));
	const canArchive = $derived(can(prefix + '.archive'));
</script>

<div class="row">
	{#if status === 'draft' && canPublish}
		<button class="btn btn-primary" type="button" onclick={() => onchange('published')}>{t('app.lifecycle.publish')}</button>
	{/if}
	{#if status === 'published' && canPublish}
		<button class="btn" type="button" onclick={() => onchange('draft')}>{t('app.lifecycle.unpublish')}</button>
	{/if}
	{#if status !== 'archived' && canArchive}
		<button class="btn btn-danger" type="button" onclick={() => confirm(t('app.common.confirm')) && onchange('archived')}>{t('app.lifecycle.archive')}</button>
	{/if}
	{#if status === 'archived' && canPublish}
		<button class="btn" type="button" onclick={() => onchange('draft')}>{t('app.lifecycle.restore')}</button>
	{/if}
</div>
