<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import { provider } from '#lib/provider/state.svelte.ts';

	interface Rec {
		id: string; badge_type: string; label: string; service_id: string | null; service_name?: string; status: string;
		submitted_at: string | null; verified_at: string | null; expires_at: string | null; rejection_reason: string | null;
		documents?: { id: string; document_type: string }[];
	}
	interface Overview { records: Rec[]; requestable: string[]; licence_services: { id: string; name: string }[]; document_types: Record<string, string[]> }

	let ov = $state<Overview | null>(null);
	let badge = $state('');
	let docType = $state('');
	let service = $state('');
	let files = $state<FileList | null>(null);
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let busy = $state(false);
	let sent = $state(false);

	async function load() {
		try {
			ov = (await api.get<Overview>('/provider/verifications')).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		if (provider.listing) load();
	});
	$effect(() => {
		docType = badge && ov ? (ov.document_types[badge]?.[0] ?? '') : '';
	});
	const contactBadges = $derived(provider.listing?.badges.filter((b) => b.type === 'phone_verified' || b.type === 'email_verified') ?? []);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!files || files.length === 0) return;
		busy = true;
		error = null;
		errors = {};
		sent = false;
		const f = new FormData();
		f.set('badge_type', badge);
		f.set('document_type', docType);
		if (badge === 'licence_checked') f.set('service_id', service);
		for (const file of Array.from(files).slice(0, 3)) f.append('files', file);
		try {
			ov = (await api.upload<Overview>('/provider/verifications', f)).data;
			sent = true;
			badge = '';
			files = null;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}
</script>

<h1>{t('app.provider.tab.verification')}</h1>
<p class="lead">{t('app.provider.lead.verification')}</p>
<p><a href="/badges" data-sveltekit-reload>{t('app.provider.badges_meaning')}</a></p>
<FormError {error} />

<section class="card">
	<h2>{t('app.provider.contact_badges')}</h2>
	{#if contactBadges.length}<ul class="list-plain">{#each contactBadges as b (b.type)}<li>✓ {b.label}</li>{/each}</ul>{/if}
	<p class="hint">{t('app.provider.contact_badges_hint')} <a href="/provider/contacts">{t('app.provider.tab.contacts')}</a></p>
</section>

{#if ov}
	<section class="card">
		<h2>{t('app.provider.requests')}</h2>
		<div class="table-wrap">
			<table>
				<thead><tr><th>{t('app.provider.badge')}</th><th>{t('app.common.status')}</th><th>{t('app.provider.dates')}</th></tr></thead>
				<tbody>
					{#each ov.records.filter((r) => r.badge_type !== 'phone_verified' && r.badge_type !== 'email_verified') as r (r.id)}
						<tr>
							<td>{r.label}{#if r.service_name} — {r.service_name}{/if}</td>
							<td><StatusBadge status={r.status} />{#if r.rejection_reason}<p class="hint">{r.rejection_reason}</p>{/if}</td>
							<td class="muted">
								{#if r.submitted_at}{t('app.provider.submitted_on', { date: formatDate(r.submitted_at) })}<br />{/if}
								{#if r.expires_at}{t('app.provider.expires_on', { date: formatDate(r.expires_at) })}{/if}
							</td>
						</tr>
					{:else}<tr><td colspan="3" class="muted">{t('app.provider.no_requests')}</td></tr>{/each}
				</tbody>
			</table>
		</div>
	</section>

	<form class="card" onsubmit={submit} novalidate>
		<h2>{t('app.provider.request_badge')}</h2>
		<p class="hint">{t('app.provider.documents_private')}</p>
		<div class="grid-2">
			<div class="field">
				<label for="v-badge">{t('app.provider.badge')}</label>
				<select id="v-badge" bind:value={badge}>
					<option value="">—</option>
					{#each ov.requestable as b (b)}<option value={b}>{t('app.provider.badge_type.' + b)}</option>{/each}
				</select>
				{#if errors.badge_type}<p class="field-error">{fieldText(errors.badge_type)}</p>{/if}
			</div>
			{#if badge}
				<div class="field">
					<label for="v-doc">{t('app.provider.document_type')}</label>
					<select id="v-doc" bind:value={docType}>
						{#each ov.document_types[badge] ?? [] as d (d)}<option value={d}>{t('app.provider.doc.' + d)}</option>{/each}
					</select>
				</div>
			{/if}
			{#if badge === 'licence_checked'}
				<div class="field">
					<label for="v-svc">{t('app.provider.licence_service')}</label>
					<select id="v-svc" bind:value={service}>
						<option value="">—</option>
						{#each ov.licence_services as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
					</select>
					{#if errors.service_id}<p class="field-error">{fieldText(errors.service_id)}</p>{/if}
				</div>
			{/if}
		</div>
		{#if badge}
			<div class="field">
				<label for="v-files">{t('app.provider.files')}</label>
				<input id="v-files" type="file" accept="application/pdf,image/jpeg,image/png,image/webp" multiple bind:files />
				<p class="hint">{t('app.provider.files_hint')}</p>
				{#if errors.files || errors.file}<p class="field-error">{fieldText(errors.files ?? errors.file)}</p>{/if}
			</div>
			<button class="btn btn-primary" type="submit" disabled={busy || !files?.length || (badge === 'licence_checked' && !service)}>
				{busy ? t('app.provider.uploading') : t('app.provider.send_for_review')}</button>
		{/if}
		{#if sent}<p class="success" role="status">{t('app.provider.request_sent')}</p>{/if}
	</form>
{/if}
