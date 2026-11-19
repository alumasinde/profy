<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';
	import { loadListing } from '#lib/provider/state.svelte.ts';

	interface Preview { display_name: string; channel: string; destination: string; expires_at: string }

	// The token arrives in the URL fragment so it never reaches server logs; it is removed
	// from the address bar immediately.
	let token = $state('');
	let preview = $state<Preview | null>(null);
	let accept = $state(false);
	let error = $state<unknown>(null);
	let busy = $state(false);

	$effect(() => {
		const m = location.hash.match(/token=([A-Za-z0-9_-]+)/);
		if (m) {
			token = m[1];
			sessionStorage.setItem('hl_claim', token);
			history.replaceState(null, '', location.pathname);
		} else {
			token = sessionStorage.getItem('hl_claim') ?? '';
		}
		if (token) api.post<Preview>('/provider/claim/preview', { token }).then((r) => (preview = r.data)).catch((e) => (error = e));
	});

	async function claim(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			await api.post('/provider/claim', { token, accept_terms: accept });
			sessionStorage.removeItem('hl_claim');
			await loadListing();
			goto('/provider');
		} catch (err) {
			error = err;
			if (err instanceof ApiError && err.code === 'claim_invalid') sessionStorage.removeItem('hl_claim');
		} finally {
			busy = false;
		}
	}
</script>

<h1>{t('app.provider.claim_title')}</h1>
<FormError {error} />
{#if !token}
	<p class="notice">{t('errors.claim_invalid')}</p>
{:else if preview}
	<form class="card narrow" onsubmit={claim} novalidate>
		<p>{t('app.provider.claim_lead', { name: preview.display_name })}</p>
		<p class="hint">{t('app.provider.claim_destination', { destination: preview.destination, date: formatDate(preview.expires_at) })}</p>
		<label class="check"><input type="checkbox" bind:checked={accept} /> {t('app.provider.accept_terms_short')}</label>
		<button class="btn btn-primary" type="submit" disabled={busy || !accept}>{t('app.provider.claim_button')}</button>
	</form>
{/if}
