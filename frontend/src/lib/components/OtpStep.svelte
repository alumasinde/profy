<script lang="ts">
	import { ApiError } from '#lib/api.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import Field from './Field.svelte';
	import FormError from './FormError.svelte';
	import type { Challenge } from '#lib/types.ts';

	let {
		challenge,
		onverify,
		onresend,
		submitLabel = ''
	}: {
		challenge: Challenge;
		onverify: (code: string) => Promise<void>;
		onresend: () => Promise<void>;
		submitLabel?: string;
	} = $props();

	let code = $state('');
	let busy = $state(false);
	let error = $state<unknown>(null);
	let wait = $state(0);

	$effect(() => {
		wait = challenge.resend_after;
		const timer = setInterval(() => (wait = Math.max(0, wait - 1)), 1000);
		return () => clearInterval(timer);
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			await onverify(code.trim());
		} catch (err) {
			error = err;
		} finally {
			busy = false;
		}
	}

	async function resend() {
		error = null;
		try {
			code = '';
			await onresend();
		} catch (err) {
			error = err;
			if (err instanceof ApiError && err.retryAfter) wait = err.retryAfter;
		}
	}
</script>

<form onsubmit={submit} novalidate>
	<p class="notice">
		{t('app.auth.code_sent', { destination: challenge.destination_masked, minutes: Math.round(challenge.expires_in / 60) })}
	</p>
	<Field id="otp-code" label={t('app.fields.code')} bind:value={code} inputmode="numeric" autocomplete="one-time-code" maxlength={6} />
	<FormError {error} />
	<button class="btn btn-primary" type="submit" disabled={busy || code.trim().length !== 6}>
		{submitLabel || t('app.auth.verify')}
	</button>
	<p>
		{#if wait > 0}
			<span class="muted">{t('app.auth.resend_in', { seconds: wait })}</span>
		{:else}
			<button class="btn-link" type="button" onclick={resend}>{t('app.auth.resend')}</button>
		{/if}
	</p>
</form>
