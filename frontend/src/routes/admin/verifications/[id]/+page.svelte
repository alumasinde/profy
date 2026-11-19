<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Doc { id: string; document_type: string; media_id: string; content_type: string; download_url?: string }
	interface Rec {
		id: string; badge_type: string; label: string; service_name?: string; status: string; submitted_at: string | null;
		verified_at: string | null; expires_at: string | null; rejection_reason: string | null; documents?: Doc[];
		provider?: { id: string; display_name: string; provider_type: string; owner_name: string };
	}
	const id = $derived(page.params.id ?? '');
	let rec = $state<Rec | null>(null);
	let error = $state<unknown>(null);
	let reason = $state('');
	let expires = $state('');
	let errors = $state<Record<string, string>>({});

	$effect(() => {
		if (id) api.get<Rec>('/admin/verifications/' + id).then((r) => (rec = r.data)).catch((e) => (error = e));
	});

	async function decide(decision: 'approve' | 'reject') {
		error = null;
		errors = {};
		const body: Record<string, unknown> = { decision, reason: reason.trim() };
		if (expires) body.expires_at = new Date(expires + 'T23:59:59+03:00').toISOString();
		try {
			rec = (await api.patch<Rec>('/admin/verifications/' + id, body)).data;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a> › <a href="/admin/verifications">{t('app.admin.verifications')}</a></p>
<FormError {error} />
{#if rec}
	<h1>{rec.label}{#if rec.service_name} — {rec.service_name}{/if} <StatusBadge status={rec.status} /></h1>
	{#if rec.provider}<p><a href={'/admin/providers/' + rec.provider.id}>{rec.provider.display_name}</a> <span class="muted">· {rec.provider.owner_name}</span></p>{/if}
	<p class="muted">{#if rec.submitted_at}{t('app.provider.submitted_on', { date: formatDate(rec.submitted_at) })}{/if}
		{#if rec.expires_at} · {t('app.provider.expires_on', { date: formatDate(rec.expires_at) })}{/if}</p>
	{#if rec.rejection_reason}<p class="notice">{rec.rejection_reason}</p>{/if}

	<section class="card">
		<h2>{t('app.admin.provider.documents')}</h2>
		<p class="hint">{t('app.admin.provider.documents_audited')}</p>
		<ul class="list-plain">
			{#each rec.documents ?? [] as d (d.id)}
				<li>{t('app.provider.doc.' + d.document_type)} ({d.content_type})
					{#if d.download_url && can('media.view_private')}
						— <a href={d.download_url + (d.content_type.startsWith('image/') ? '?inline=1' : '')} target="_blank" rel="noopener" data-sveltekit-reload>{t('app.admin.provider.open_document')}</a>
					{/if}</li>
			{:else}<li class="muted">{t('app.common.none')}</li>{/each}
		</ul>
	</section>

	{#if rec.status === 'pending' && (can('providers.verify') || can('providers.reject_verification'))}
		<section class="card">
			<h2>{t('app.admin.provider.decision')}</h2>
			{#if rec.badge_type === 'licence_checked'}
				<div class="field narrow">
					<label for="d-exp">{t('app.admin.provider.licence_expiry')}</label>
					<input id="d-exp" type="date" bind:value={expires} />
					{#if errors.expires_at}<p class="field-error">{fieldText(errors.expires_at)}</p>{/if}
				</div>
			{/if}
			<div class="field">
				<label for="d-reason">{t('app.admin.provider.reason_label')}</label>
				<textarea id="d-reason" rows="2" maxlength="500" bind:value={reason}></textarea>
				{#if errors.reason}<p class="field-error">{fieldText(errors.reason)}</p>{/if}
			</div>
			<div class="row">
				{#if can('providers.verify')}<button class="btn btn-primary" type="button" onclick={() => decide('approve')}>{t('app.admin.provider.action.approve')}</button>{/if}
				{#if can('providers.reject_verification')}<button class="btn btn-danger" type="button" onclick={() => decide('reject')}>{t('app.admin.provider.action.reject')}</button>{/if}
			</div>
		</section>
	{/if}
{/if}
