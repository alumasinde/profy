<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import AreasEditor from '#lib/provider/AreasEditor.svelte';
	import ContactsEditor from '#lib/provider/ContactsEditor.svelte';
	import PhotosEditor from '#lib/provider/PhotosEditor.svelte';
	import ProfileForm from '#lib/provider/ProfileForm.svelte';
	import ServicesEditor from '#lib/provider/ServicesEditor.svelte';
	import { formatKES, staffTarget, type Listing, type Offered } from '#lib/provider/types.ts';
	import { can } from '#lib/session.svelte.ts';

	const id = $derived(page.params.id ?? '');
	const target = $derived(staffTarget(id));
	let l = $state<Listing | null>(null);
	let error = $state<unknown>(null);
	let reason = $state('');
	let reasonErr = $state('');
	let invite = $state({ channel: 'sms', destination: '' });
	let inviteErr = $state('');
	let invited = $state(false);

	async function load() {
		try {
			l = (await api.get<Listing>('/admin/providers/' + id)).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		if (id) load();
	});
	const set = (v: Listing) => (l = v);

	// Display only: the API checks each transition's permission and source state.
	const actions = $derived.by(() => {
		if (!l) return [];
		const s = l.status;
		const out: { action: string; perm: string; reason: boolean; danger?: boolean }[] = [];
		if (s === 'pending_review') out.push({ action: 'approve', perm: 'providers.publish_any', reason: false }, { action: 'reject', perm: 'providers.publish_any', reason: true });
		if (s === 'draft' && l.owner) out.push({ action: 'submit', perm: 'providers.update_any', reason: false });
		if (s === 'published') out.push({ action: 'pause', perm: 'providers.publish_any', reason: false });
		if (s === 'paused') out.push({ action: 'resume', perm: 'providers.publish_any', reason: false });
		if (['pending_review', 'published', 'paused'].includes(s)) out.push({ action: 'suspend', perm: 'providers.suspend', reason: true, danger: true });
		if (s === 'suspended') out.push({ action: 'restore', perm: 'providers.restore', reason: false });
		if (s !== 'archived') out.push({ action: 'archive', perm: 'providers.update_any', reason: false, danger: true });
		return out.filter((a) => can(a.perm));
	});

	async function act(a: { action: string; reason: boolean }) {
		reasonErr = '';
		if (a.reason && !reason.trim()) return (reasonErr = 'required');
		if (a.action === 'archive' && !confirm(t('app.provider.confirm_archive'))) return;
		error = null;
		try {
			l = (await api.post<Listing>('/admin/providers/' + id + '/status', { action: a.action, reason: reason.trim() })).data;
			reason = '';
		} catch (err) {
			error = err;
			if (err instanceof ApiError && err.fields.reason) reasonErr = err.fields.reason;
		}
	}

	async function sendInvite(e: SubmitEvent) {
		e.preventDefault();
		inviteErr = '';
		invited = false;
		error = null;
		try {
			l = (await api.post<Listing>('/admin/providers/' + id + '/invite', invite)).data;
			invited = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) inviteErr = err.fields.destination ?? '';
		}
	}

	function price(o: Offered) {
		if (o.pricing_type === 'on_quote') return t('app.provider.price_on_quote');
		if (o.pricing_type === 'range') return t('app.provider.price_range', { from: formatKES(o.price_from), to: formatKES(o.price_to) });
		return t('app.provider.price_' + o.pricing_type, { amount: formatKES(o.price_from) });
	}
	const editable = $derived(can('providers.update_any') && l?.status !== 'archived');
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a> › <a href="/admin/providers">{t('app.admin.providers')}</a></p>
<FormError {error} />
{#if l}
	<h1>{l.display_name} <StatusBadge status={l.status} /></h1>
	<p class="muted">{t('app.provider.type_' + l.provider_type)} · {l.base_area?.label ?? '—'} · {t('app.provider.completeness', { score: l.completeness.score })}
		{#if l.submitted_at} · {t('app.provider.submitted_on', { date: formatDate(l.submitted_at) })}{/if}</p>
	<p class="row">
		<a class="btn" href={'/preview/professionals/' + l.id} data-sveltekit-reload target="_blank" rel="noopener">{t('app.common.preview')}</a>
		{#if l.status === 'published'}<a class="btn" href={l.path} data-sveltekit-reload target="_blank" rel="noopener">{t('app.common.view_public')}</a>{/if}
	</p>
	{#if l.status_reason}<p class="notice"><strong>{t('app.provider.reason')}:</strong> {l.status_reason}</p>{/if}

	{#if actions.length}
		<section class="card" aria-labelledby="mod-h">
			<h2 id="mod-h">{t('app.admin.provider.moderation')}</h2>
			{#if actions.some((a) => a.reason)}
				<div class="field">
					<label for="m-reason">{t('app.admin.provider.reason_label')}</label>
					<textarea id="m-reason" rows="2" maxlength="500" bind:value={reason} aria-invalid={reasonErr ? 'true' : undefined}></textarea>
					<p class="hint">{t('app.admin.provider.reason_hint')}</p>
					{#if reasonErr}<p class="field-error">{fieldText(reasonErr)}</p>{/if}
				</div>
			{/if}
			<div class="row">
				{#each actions as a (a.action)}
					<button class={'btn ' + (a.action === 'approve' ? 'btn-primary' : a.danger ? 'btn-danger' : '')} type="button" onclick={() => act(a)}>
						{t('app.admin.provider.action.' + a.action)}</button>
				{/each}
			</div>
			{#if l.status === 'pending_review' && !l.completeness.can_submit}<p class="field-error">{t('errors.listing_incomplete')}</p>{/if}
		</section>
	{/if}

	<div class="grid-2">
		<section class="card">
			<h2>{t('app.admin.provider.owner')}</h2>
			{#if l.owner}
				<p><a href={'/admin/users/' + l.owner.public_id}>{l.owner.display_name}</a></p>
				<p class="muted">{l.owner.phone ?? ''} {l.owner.email ?? ''}</p>
			{:else}
				<p class="muted">{t('app.admin.provider.no_owner')}</p>
			{/if}
			{#if l.claim}
				<p class="hint">{l.claim.claimed_at ? t('app.admin.provider.claimed_on', { date: formatDate(l.claim.claimed_at) })
					: t('app.admin.provider.invite_pending', { destination: l.claim.destination, date: formatDate(l.claim.expires_at) })}</p>
			{/if}
			{#if !l.owner && editable}
				<form onsubmit={sendInvite} novalidate>
					<div class="grid-2">
						<div class="field">
							<label for="i-ch">{t('app.admin.provider.invite_channel')}</label>
							<select id="i-ch" bind:value={invite.channel}>
								<option value="sms">{t('app.admin.channel.sms')}</option>
								<option value="email">{t('app.admin.channel.email')}</option>
							</select>
						</div>
						<div class="field">
							<label for="i-dest">{t('app.admin.provider.invite_destination')}</label>
							<input id="i-dest" bind:value={invite.destination} />
							{#if inviteErr}<p class="field-error">{fieldText(inviteErr)}</p>{/if}
						</div>
					</div>
					<button class="btn" type="submit" disabled={!invite.destination.trim()}>{t('app.admin.provider.send_invite')}</button>
					{#if invited}<p class="success" role="status">{t('app.admin.provider.invite_sent')}</p>{/if}
				</form>
			{/if}
		</section>
		<section class="card">
			<h2>{t('app.admin.provider.badges')}</h2>
			{#if l.badges.length}<ul class="list-plain">{#each l.badges as b (b.type + (b.service_name ?? ''))}<li>✓ {b.label}{#if b.service_name} ({b.service_name}){/if}</li>{/each}</ul>
			{:else}<p class="muted">{t('app.common.none')}</p>{/if}
			<a href="/admin/verifications">{t('app.admin.verifications')}</a>
		</section>
	</div>

	<section class="card">
		<h2>{t('app.admin.provider.summary')}</h2>
		<ul class="list-plain checklist">
			{#each l.completeness.items as it (it.key)}<li>{it.done ? '✓' : '○'} {t('app.provider.item.' + it.key)}{#if it.required && !it.done} <span class="badge status-draft">{t('app.provider.required')}</span>{/if}</li>{/each}
		</ul>
		{#if l.description}<h3>{t('app.admin.provider.about')}</h3><p class="pre">{l.description}</p>{/if}
		<h3>{t('app.provider.tab.services')}</h3>
		<ul>{#each l.services as o (o.id)}<li>{o.custom_title || o.service_name} — {price(o)}{#if o.status === 'hidden'} ({t('app.provider.offer_hidden')}){/if}</li>{:else}<li class="muted">{t('app.provider.no_services')}</li>{/each}</ul>
		<h3>{t('app.admin.provider.areas')}</h3>
		<p>{l.coverage.map((c) => c.label).join(' · ') || '—'}{#if l.service_radius_km} — {t('app.provider.radius_km', { km: l.service_radius_km })}{/if}</p>
		<h3>{t('app.provider.tab.contacts')}</h3>
		<ul>{#each l.contacts as c (c.channel)}<li>{t('app.provider.channel.' + c.channel)}: {c.value} · {t('app.provider.vis.' + c.visibility)}{#if c.verified_at} · ✓{/if}</li>{/each}</ul>
		{#if l.portfolio.length || l.profile_photo}
			<h3>{t('app.provider.tab.photos')}</h3>
			<ul class="thumbs">
				{#if l.profile_photo}<li><img src={l.profile_photo.urls?.['160'] ?? l.profile_photo.url} alt={t('app.provider.photo.profile')} /></li>{/if}
				{#each l.portfolio as p (p.id)}<li><img src={p.media.urls?.['160'] ?? p.media.url} alt={p.caption ?? ''} />{#if p.media.status !== 'active'}<StatusBadge status={p.media.status} />{/if}</li>{/each}
			</ul>
		{/if}
	</section>

	{#if editable}
		<h2>{t('app.admin.provider.edit')}</h2>
		<details><summary>{t('app.provider.tab.profile')}</summary><ProfileForm {target} listing={l} onsaved={set} /></details>
		<details><summary>{t('app.provider.tab.services')}</summary><ServicesEditor {target} listing={l} onsaved={set} /></details>
		<details><summary>{t('app.provider.tab.areas')}</summary><AreasEditor {target} listing={l} onsaved={set} /></details>
		<details><summary>{t('app.provider.tab.contacts')}</summary><ContactsEditor {target} listing={l} onsaved={set} /></details>
		<details><summary>{t('app.provider.tab.photos')}</summary><PhotosEditor {target} listing={l} onsaved={set} /></details>
	{/if}

	{#if l.history?.length}
		<section class="card">
			<h2>{t('app.admin.provider.history')}</h2>
			<ol class="history">
				{#each l.history as h, i (i)}
					<li><span class="muted">{formatDate(h.at)}</span> — {h.from ? t('app.status.' + h.from) + ' → ' : ''}{t('app.status.' + h.to)}
						{#if h.actor_name} · {h.actor_name}{/if}{#if h.reason}<br /><span class="hint">{h.reason}</span>{/if}</li>
				{/each}
			</ol>
		</section>
	{/if}
{/if}

<style>
	.thumbs { list-style: none; padding: 0; display: flex; flex-wrap: wrap; gap: 0.5rem; }
	.thumbs img { width: 96px; height: 72px; object-fit: cover; border-radius: 4px; display: block; }
	.history { list-style: none; padding: 0; margin: 0; }
	.history li { padding: 0.35rem 0; border-bottom: 1px solid var(--color-border, #e2e8f0); }
	details { margin: 0.5rem 0; }
	summary { cursor: pointer; font-weight: 600; padding: 0.5rem 0; }
	.pre { white-space: pre-line; }
</style>
