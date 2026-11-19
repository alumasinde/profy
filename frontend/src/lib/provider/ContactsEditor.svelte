<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import OtpStep from '#lib/components/OtpStep.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import type { Challenge } from '#lib/types.ts';
	import type { Contact, EditorTarget, Listing } from './types.ts';

	let { target, listing, onsaved }: { target: EditorTarget; listing: Listing; onsaved: (l: Listing) => void } = $props();

	const channels = ['phone', 'whatsapp', 'email'] as const;
	type Row = { enabled: boolean; value: string; visibility: Contact['visibility'] };
	function rowsFor(l: Listing): Record<string, Row> {
		const r: Record<string, Row> = {};
		for (const ch of channels) {
			const c = l.contacts.find((x) => x.channel === ch);
			r[ch] = { enabled: !!c, value: c?.value ?? '', visibility: c?.visibility ?? 'reveal_on_click' };
		}
		return r;
	}
	// svelte-ignore state_referenced_locally
	let rows = $state<Record<string, Row>>(rowsFor(listing));
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let saved = $state(false);
	let busy = $state(false);
	let challenge = $state<Challenge | null>(null);
	let verifying = $state<string>('');

	$effect(() => {
		rows = rowsFor(listing);
	});
	const current = (ch: string) => listing.contacts.find((x) => x.channel === ch);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		saved = false;
		error = null;
		errors = {};
		const list = channels.filter((ch) => rows[ch].enabled).map((ch) => ({ channel: ch, value: rows[ch].value, visibility: rows[ch].visibility }));
		try {
			onsaved((await api.put<Listing>(target.base + '/contacts', { contacts: list })).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) {
				// Server keys are contacts.<index>.<field>; map them back to channels.
				const enabled = channels.filter((ch) => rows[ch].enabled);
				const mapped: Record<string, string> = {};
				for (const [k, v] of Object.entries(err.fields)) {
					const m = k.match(/^contacts\.(\d+)\.(\w+)$/);
					if (m) mapped[enabled[Number(m[1])] + '.' + m[2]] = v;
				}
				errors = mapped;
			}
		} finally {
			busy = false;
		}
	}

	async function startVerify(ch: string) {
		error = null;
		try {
			challenge = (await api.post<Challenge>('/provider/contacts/verify', { channel: ch })).data;
			verifying = ch;
		} catch (err) {
			error = err;
		}
	}
	async function confirm(code: string) {
		if (!challenge) return;
		onsaved((await api.post<Listing>('/provider/contacts/verify/confirm', { challenge_id: challenge.challenge_id, code })).data);
		challenge = null;
		verifying = '';
	}
</script>

<form onsubmit={save} novalidate>
	{#each channels as ch (ch)}
		{@const c = current(ch)}
		<section class="card">
			<label class="check"><input type="checkbox" bind:checked={rows[ch].enabled} /> <strong>{t('app.provider.channel.' + ch)}</strong></label>
			{#if rows[ch].enabled}
				<div class="grid-2">
					<div class="field">
						<label for={'c-' + ch}>{t('app.provider.contact_value.' + ch)}</label>
						<input id={'c-' + ch} type={ch === 'email' ? 'email' : 'tel'} inputmode={ch === 'email' ? 'email' : 'tel'}
							autocomplete={ch === 'email' ? 'email' : 'tel'} bind:value={rows[ch].value} aria-invalid={errors[ch + '.value'] ? 'true' : undefined} />
						{#if errors[ch + '.value']}<p class="field-error">{fieldText(errors[ch + '.value'])}</p>{/if}
					</div>
					<div class="field">
						<label for={'v-' + ch}>{t('app.provider.visibility')}</label>
						<select id={'v-' + ch} bind:value={rows[ch].visibility}>
							{#each ['public', 'reveal_on_click', 'logged_in_only', 'hidden'] as v (v)}<option value={v}>{t('app.provider.vis.' + v)}</option>{/each}
						</select>
						<p class="hint">{t('app.provider.vis_hint.' + rows[ch].visibility)}</p>
					</div>
				</div>
				{#if c && c.value === rows[ch].value}
					{#if c.verified_at}<p class="success">✓ {t('app.provider.contact_verified')}</p>
					{:else if ch !== 'whatsapp'}
						<p class="muted">{t('app.provider.contact_unverified')}
							{#if !target.staff}<button class="btn-link" type="button" onclick={() => startVerify(ch)}>{t('app.provider.verify_now')}</button>{/if}</p>
					{/if}
				{:else if c}<p class="hint">{t('app.provider.contact_changed')}</p>{/if}
			{/if}
		</section>
	{/each}
	<FormError {error} />
	{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
	<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? t('app.common.saving') : t('app.common.save')}</button>
</form>

{#if challenge}
	<section class="card" aria-live="polite">
		<h2>{t('app.provider.verify_title', { channel: t('app.provider.channel.' + verifying) })}</h2>
		<OtpStep {challenge} onverify={confirm} onresend={() => startVerify(verifying)} />
	</section>
{/if}
