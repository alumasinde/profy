<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, safeNext } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import OtpStep from '#lib/components/OtpStep.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { refreshSession } from '#lib/session.svelte.ts';
	import type { Challenge } from '#lib/types.ts';

	let mode = $state<'code' | 'pin'>('code');
	let destination = $state('');
	let pin = $state('');
	let challenge = $state<Challenge | null>(null);
	let busy = $state(false);
	let error = $state<unknown>(null);
	let fieldErrors = $state<Record<string, string>>({});

	const next = $derived(safeNext(page.url.searchParams.get('next')));

	async function done() {
		await refreshSession();
		goto(next, { replaceState: true });
	}

	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = null;
		fieldErrors = {};
		try {
			const res = await api.post<Challenge>('/auth/otp/send', { destination, purpose: 'login' });
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
		await done();
	}

	async function pinLogin(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			await api.post('/auth/pin/login', { destination, pin });
			await done();
		} catch (err) {
			error = err;
		} finally {
			busy = false;
		}
	}
</script>

<div class="narrow">
	<h1>{t('app.auth.login_title')}</h1>
	<div class="card">
		{#if challenge}
			<OtpStep {challenge} onverify={verify} onresend={() => send()} />
			<button class="btn-link" type="button" onclick={() => (challenge = null)}>{t('app.common.back')}</button>
		{:else}
			<div class="tabs" role="tablist">
				<button type="button" role="tab" aria-selected={mode === 'code'} onclick={() => (mode = 'code')}>{t('app.auth.use_code')}</button>
				<button type="button" role="tab" aria-selected={mode === 'pin'} onclick={() => (mode = 'pin')}>{t('app.auth.use_pin')}</button>
			</div>
			{#if mode === 'code'}
				<form onsubmit={(e) => send(e).catch(() => {})} novalidate>
					<Field id="destination" label={t('app.fields.destination')} hint={t('app.fields.destination_hint')}
						bind:value={destination} autocomplete="username" error={fieldErrors.destination} />
					<FormError {error} />
					<button class="btn btn-primary" type="submit" disabled={busy || !destination}>{t('app.auth.send_code')}</button>
				</form>
			{:else}
				<form onsubmit={pinLogin} novalidate>
					<p class="muted">{t('app.auth.pin_device_note')}</p>
					<Field id="pin-destination" label={t('app.fields.destination')} bind:value={destination} autocomplete="username" />
					<Field id="pin" label={t('app.fields.pin')} type="password" inputmode="numeric" maxlength={6}
						autocomplete="current-password" bind:value={pin} />
					<FormError {error} />
					<button class="btn btn-primary" type="submit" disabled={busy || !destination || pin.length !== 6}>{t('app.auth.pin_login')}</button>
				</form>
			{/if}
		{/if}
	</div>
	<p>{t('app.auth.no_account')} <a href="/auth/register">{t('app.nav.register')}</a></p>
	<p>{t('app.auth.forgot')} <a href="/auth/recover">{t('app.auth.recover_title')}</a></p>
</div>
