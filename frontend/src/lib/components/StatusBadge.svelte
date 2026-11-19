<script lang="ts">
	import { t } from '#lib/i18n.svelte.ts';
	let { status }: { status: string } = $props();
</script>

<span class="badge status-{status}">{t('app.status.' + status)}</span>
