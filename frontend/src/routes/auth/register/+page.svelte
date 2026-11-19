<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, safeNext } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import OtpStep from '#lib/components/OtpStep.svelte';
	import { i18n, t } from '#lib/i18n.svelte.ts';
	import { refreshSession } from '#lib/session.svelte.ts';
	import type { Challenge } from '#lib/types.ts';

	let form = $state({ first_name: '', middle_name: '', last_name: '', destination: '', accept_terms: false });
	let challenge = $state<Challenge | null>(null);
	let busy = $state(false);
	let error = $state<unknown>(null);
	let fieldErrors = $state<Record<string, string>>({});

	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = null;
		fieldErrors = {};
		try {
			const res = await api.post<Challenge>('/auth/register', {
				...form,
				middle_name: form.middle_name.trim() === '' ? null : form.middle_name,
				locale: i18n.locale
			});
			challenge = res.data;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) fieldErrors = err.fields;
			throw err;
		} finally {
			busy = false;
		}
	}

	async function verify(code: string) {
		await api.post('/auth/otp/verify', { challenge_id: challenge!.challenge_id, code, device_name: navigator.platform || '' });
		await refreshSession();
		goto(safeNext(page.url.searchParams.get('next')), { replaceState: true });
	}
</script>

<div class="narrow">
	<h1>{t('app.auth.register_title')}</h1>
	<div class="card">
		{#if challenge}
			<OtpStep {challenge} onverify={verify} onresend={() => send()} />
			<button class="btn-link" type="button" onclick={() => (challenge = null)}>{t('app.common.back')}</button>
		{:else}
			<form onsubmit={(e) => send(e).catch(() => {})} novalidate>
				<Field id="first_name" label={t('app.fields.first_name')} autocomplete="given-name" bind:value={form.first_name} error={fieldErrors.first_name} />
				<Field id="middle_name" label={t('app.fields.middle_name')} autocomplete="additional-name" optional bind:value={form.middle_name} error={fieldErrors.middle_name} />
				<Field id="last_name" label={t('app.fields.last_name')} autocomplete="family-name" bind:value={form.last_name} error={fieldErrors.last_name} />
				<Field id="destination" label={t('app.fields.destination')} hint={t('app.fields.destination_hint')} autocomplete="username"
					bind:value={form.destination} error={fieldErrors.destination} />
				<label class="checkbox">
					<input type="checkbox" bind:checked={form.accept_terms} aria-invalid={fieldErrors.accept_terms ? 'true' : undefined} />
					<span>{t('app.fields.accept_terms')}</span>
				</label>
				<FormError {error} />
				<button class="btn btn-primary" type="submit" disabled={busy}>{t('app.common.continue')}</button>
			</form>
		{/if}
	</div>
	<p>{t('app.auth.have_account')} <a href="/auth/login">{t('app.nav.login')}</a></p>
</div>
