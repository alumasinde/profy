<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import Completeness from '#lib/provider/Completeness.svelte';
	import { provider, setListing } from '#lib/provider/state.svelte.ts';
	import type { Listing } from '#lib/provider/types.ts';

	let form = $state({ provider_type: 'individual', display_name: '', accept_terms: false });
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let busy = $state(false);
	let stats = $state<{ profile_views_30d: number; contact_reveals_30d: Record<string, number> } | null>(null);

	const l = $derived(provider.listing);
	$effect(() => {
		if (l && l.published_at && l.status !== 'archived') api.get<typeof stats>('/provider/dashboard').then((r) => (stats = r.data)).catch(() => {});
	});

	async function start(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		errors = {};
		try {
			setListing((await api.post<Listing>('/provider/profile', form)).data);
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}

	async function act(action: 'submit' | 'pause' | 'resume' | 'archive') {
		if ((action === 'archive' || action === 'pause') && !confirm(t('app.provider.confirm_' + action))) return;
		error = null;
		try {
			setListing((await api.post<Listing>('/provider/profile/' + action)).data);
		} catch (err) {
			error = err;
		}
	}
	const missing = $derived(l ? l.completeness.items.filter((i) => i.required && !i.done) : []);
</script>

{#if !l}
	<h1>{t('app.provider.start_title')}</h1>
	<p class="lead">{t('app.provider.start_lead')}</p>
	<form class="card narrow" onsubmit={start} novalidate>
		<fieldset class="field">
			<legend>{t('app.provider.type')}</legend>
			<label class="check"><input type="radio" name="t" value="individual" bind:group={form.provider_type} /> {t('app.provider.type_individual')}</label>
			<label class="check"><input type="radio" name="t" value="business" bind:group={form.provider_type} /> {t('app.provider.type_business')}</label>
		</fieldset>
		<Field id="st-name" label={t('app.provider.display_name')} bind:value={form.display_name} maxlength={120} error={errors.display_name}
			hint={t('app.provider.display_name_hint')} />
		<label class="check"><input type="checkbox" bind:checked={form.accept_terms} /> {t('app.provider.accept_terms_short')}</label>
		{#if errors.accept_terms}<p class="field-error">{fieldText(errors.accept_terms)}</p>{/if}
		<FormError {error} />
		<button class="btn btn-primary" type="submit" disabled={busy || !form.display_name.trim()}>{t('app.provider.start_button')}</button>
		<p class="hint">{t('app.provider.claim_hint')}</p>
	</form>
{:else}
	<h1>{l.display_name} <StatusBadge status={l.status} /></h1>
	<FormError {error} />
	<section class="card" aria-live="polite">
		<p>{t('app.provider.status_help.' + l.status)}</p>
		{#if l.status_reason && (l.status === 'draft' || l.status === 'suspended')}
			<p class="notice"><strong>{t('app.provider.reason')}:</strong> {l.status_reason}</p>
		{/if}
		{#if l.submitted_at && l.status === 'pending_review'}<p class="muted">{t('app.provider.submitted_on', { date: formatDate(l.submitted_at) })}</p>{/if}
		<div class="row">
			{#if l.status === 'draft'}
				<button class="btn btn-primary" type="button" onclick={() => act('submit')} disabled={!l.completeness.can_submit}>{t('app.provider.submit')}</button>
			{/if}
			{#if l.status === 'published'}<button class="btn" type="button" onclick={() => act('pause')}>{t('app.provider.pause')}</button>{/if}
			{#if l.status === 'paused'}<button class="btn btn-primary" type="button" onclick={() => act('resume')}>{t('app.provider.resume')}</button>{/if}
			<a class="btn" href={'/preview/professionals/' + l.id} data-sveltekit-reload>{t('app.common.preview')}</a>
			{#if l.status === 'published'}<a class="btn" href={l.path} data-sveltekit-reload>{t('app.common.view_public')}</a>{/if}
		</div>
		{#if l.status === 'draft' && missing.length}
			<p class="hint">{t('app.provider.missing', { items: missing.map((m) => t('app.provider.item.' + m.key)).join(', ') })}</p>
		{/if}
	</section>

	{#if stats}
		<section class="card">
			<h2>{t('app.provider.stats_title')}</h2>
			<p>{t('app.provider.stats_views', { n: stats.profile_views_30d })}</p>
			<p>{t('app.provider.stats_reveals', {
				phone: stats.contact_reveals_30d.phone_reveal ?? 0,
				whatsapp: stats.contact_reveals_30d.whatsapp_click ?? 0,
				email: stats.contact_reveals_30d.email_reveal ?? 0 })}</p>
		</section>
	{/if}

	<Completeness listing={l} />

	{#if l.badges.length}
		<section class="card">
			<h2>{t('app.provider.badges')}</h2>
			<ul class="list-plain">{#each l.badges as b (b.type + (b.service_name ?? ''))}<li>✓ {b.label}{#if b.service_name} ({b.service_name}){/if}</li>{/each}</ul>
			<a href="/provider/verification">{t('app.provider.get_verified')}</a>
		</section>
	{/if}

	{#if l.status !== 'archived'}
		<section class="card danger-zone">
			<h2>{t('app.provider.archive_title')}</h2>
			<p class="hint">{t('app.provider.archive_hint')}</p>
			<button class="btn btn-danger" type="button" onclick={() => act('archive')}>{t('app.lifecycle.archive')}</button>
		</section>
	{/if}
{/if}
