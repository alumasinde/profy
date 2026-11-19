<script lang="ts">
	import { ApiError } from '#lib/api.ts';
	import { errorText } from '#lib/i18n.svelte.ts';

	let { error }: { error: unknown } = $props();
	const code = $derived(error instanceof ApiError ? error.code : error ? 'internal_error' : '');
</script>

{#if code}
	<p class="form-error" role="alert">{errorText(code)}</p>
{/if}
