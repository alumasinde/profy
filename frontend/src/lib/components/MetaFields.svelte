<script lang="ts">
	import { t } from '#lib/i18n.svelte.ts';
	import Field from './Field.svelte';

	let {
		meta_title = $bindable(''),
		meta_description = $bindable(''),
		errors = {}
	}: { meta_title?: string; meta_description?: string; errors?: Record<string, string> } = $props();
</script>

<details class="seo">
	<summary>{t('app.common.seo')}</summary>
	<Field id="meta_title" label={t('app.common.meta_title')} optional maxlength={160} bind:value={meta_title} error={errors.meta_title} />
	<Field id="meta_description" label={t('app.common.meta_description')} optional rows={2} maxlength={320} bind:value={meta_description} error={errors.meta_description} />
</details>
