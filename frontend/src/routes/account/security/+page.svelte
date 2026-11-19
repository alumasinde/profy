<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import OtpStep from '#lib/components/OtpStep.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';
	import { refreshSession, session } from '#lib/session.svelte.ts';
	import type { Challenge } from '#lib/types.ts';

	interface SessionRow {
		id: string;
		auth_method: string;
		device_name: string;
		user_agent: string;
		created_at: string;
		last_seen_at: string;
		is_current: boolean;
	}

	let sessions = $state<SessionRow[]>([]);
	let listError = $state<unknown>(null);
	let pin = $state('');
	let pinOpen = $state(false);
	let pinSaved = $state(false);
	let pinError = $state<unknown>(null);
	let pinFieldErr = $state('');
	let reauth = $state<Challenge | null>(null);
	let busy = $state(false);

	const reauthValid = $derived(!!session.reauthUntil && new Date(session.reauthUntil).getTime() > Date.now());

	async function load() {
		try {
			sessions = (await api.get<SessionRow[]>('/auth/sessions')).data;
		} catch (err) {
			listError = err;
		}
	}
	$effect(() => {
		load();
	});

	async function revoke(id: string) {
		await api.del('/auth/sessions/' + encodeURIComponent(id)).catch((e) => (listError = e));
		await load();
	}
	async function revokeOthers() {
		await api.del('/auth/sessions').catch((e) => (listError = e));
		await load();
	}

	async function startReauth() {
		pinError = null;
		try {
			reauth = (await api.post<Challenge>('/auth/otp/send', { purpose: 'sensitive_change' })).data;
		} catch (err) {
			pinError = err;
			throw err;
		}
	}
	async function verifyReauth(code: string) {
		await api.post('/auth/otp/verify', { challenge_id: reauth!.challenge_id, code });
		reauth = null;
		await refreshSession();
	}

	async function savePin(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		pinError = null;
		pinFieldErr = '';
		try {
			await api.post('/auth/pin/setup', { pin });
			pin = '';
			pinOpen = false;
			pinSaved = true;
			await refreshSession();
		} catch (err) {
			pinError = err;
			if (err instanceof ApiError) {
				pinFieldErr = err.fields.pin ?? '';
				if (err.code === 'reauthentication_required') await refreshSession();
			}
		} finally {
			busy = false;
		}
	}
</script>

<h1>{t('app.security.title')}</h1>

<section class="card">
	<h2>{t('app.security.pin_title')}</h2>
	<p>{session.hasPIN ? t('app.security.pin_set') : t('app.security.pin_not_set')}</p>
	{#if pinSaved}<p class="success" role="status">{t('app.security.pin_saved')}</p>{/if}
	{#if !pinOpen}
		<button class="btn" type="button" onclick={() => { pinOpen = true; pinSaved = false; }}>
			{session.hasPIN ? t('app.security.pin_change') : t('app.security.pin_setup')}
		</button>
	{:else if !reauthValid}
		<h3>{t('app.security.reauth_title')}</h3>
		{#if reauth}
			<OtpStep challenge={reauth} onverify={verifyReauth} onresend={startReauth} />
		{:else}
			<p class="muted">{t('app.security.reauth_body')}</p>
			<FormError error={pinError} />
			<button class="btn btn-primary" type="button" onclick={() => startReauth().catch(() => {})}>{t('app.security.reauth_send')}</button>
		{/if}
	{:else}
		<form onsubmit={savePin} novalidate>
			<Field id="new-pin" label={t('app.fields.new_pin')} type="password" inputmode="numeric" maxlength={6}
				autocomplete="new-password" bind:value={pin} error={pinFieldErr} />
			<FormError error={pinError} />
			<div class="row">
				<button class="btn btn-primary" type="submit" disabled={busy || pin.length !== 6}>{t('app.common.save')}</button>
				<button class="btn" type="button" onclick={() => (pinOpen = false)}>{t('app.common.cancel')}</button>
			</div>
		</form>
	{/if}
</section>

<section class="card">
	<h2>{t('app.security.sessions_title')}</h2>
	<FormError error={listError} />
	<ul class="plain">
		{#each sessions as s (s.id)}
			<li class="card">
				<strong>{s.device_name || s.user_agent || '—'}</strong>
				{#if s.is_current}<span class="badge">{t('app.security.current')}</span>{/if}
				<span class="badge">{t('app.security.method.' + s.auth_method)}</span>
				<p class="muted">{t('app.security.signed_in', { date: formatDate(s.created_at) })} · {t('app.security.last_active', { date: formatDate(s.last_seen_at) })}</p>
				{#if !s.is_current}<button class="btn btn-danger" type="button" onclick={() => revoke(s.id)}>{t('app.security.revoke')}</button>{/if}
			</li>
		{/each}
	</ul>
	{#if sessions.length > 1}
		<button class="btn btn-danger" type="button" onclick={revokeOthers}>{t('app.security.revoke_others')}</button>
	{/if}
</section>

<style>
	.plain {
		list-style: none;
		padding: 0;
		margin: 0;
	}
</style>
