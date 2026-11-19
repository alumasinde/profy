<script lang="ts">
	import { page } from '$app/state';
	import { t } from '#lib/i18n.svelte.ts';
</script>

<h1>{page.status === 404 ? t('app.not_found') : t('app.error_title')}</h1>
<p><a href="/" data-sveltekit-reload>{t('public.not_found.home_link')}</a></p>
