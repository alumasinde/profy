<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import OtpStep from '#lib/components/OtpStep.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { refreshSession } from '#lib/session.svelte.ts';
	import type { Challenge } from '#lib/types.ts';

	let destination = $state('');
	let setPin = $state(false);
	let newPin = $state('');
	let challenge = $state<Challenge | null>(null);
	let busy = $state(false);
	let error = $state<unknown>(null);
	let fieldErrors = $state<Record<string, string>>({});

	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = null;
		try {
			challenge = (await api.post<Challenge>('/auth/recovery/start', { destination })).data;
		} catch (err) {
			error = err;
			throw err;
		} finally {
			busy = false;
		}
	}

	async function complete(code: string) {
		fieldErrors = {};
		try {
			await api.post('/auth/recovery/complete', {
				challenge_id: challenge!.challenge_id,
				code,
				new_pin: setPin ? newPin : null,
				device_name: navigator.platform || ''
			});
		} catch (err) {
			if (err instanceof ApiError) fieldErrors = err.fields;
			throw err;
		}
		await refreshSession();
		goto('/account/security', { replaceState: true });
	}
</script>

<div class="narrow">
	<h1>{t('app.auth.recover_title')}</h1>
	<div class="card">
		{#if challenge}
			<label class="checkbox">
				<input type="checkbox" bind:checked={setPin} />
				<span>{t('app.auth.set_new_pin')}</span>
			</label>
			{#if setPin}
				<Field id="new_pin" label={t('app.fields.new_pin')} type="password" inputmode="numeric" maxlength={6}
					autocomplete="new-password" bind:value={newPin} error={fieldErrors.new_pin} />
			{/if}
			<OtpStep {challenge} onverify={complete} onresend={() => send()} submitLabel={t('app.auth.recover_submit')} />
		{:else}
			<p class="muted">{t('app.auth.recover_hint')}</p>
			<form onsubmit={(e) => send(e).catch(() => {})} novalidate>
				<Field id="destination" label={t('app.fields.destination')} hint={t('app.fields.destination_hint')} bind:value={destination} autocomplete="username" />
				<FormError {error} />
				<button class="btn btn-primary" type="submit" disabled={busy || !destination}>{t('app.auth.send_code')}</button>
			</form>
		{/if}
	</div>
</div>
