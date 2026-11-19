<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Item {
		id: string; display_name: string; provider_type: string; slug: string; status: string; completeness: number;
		assisted: boolean; claimed: boolean; base_area: string | null; submitted_at: string | null; updated_at: string;
	}
	interface Page { items: Item[]; next_cursor?: string; counts: Record<string, number> }
	const statuses = ['pending_review', 'published', 'draft', 'paused', 'suspended', 'archived'];

	let status = $state('pending_review');
	let q = $state('');
	let data = $state<Page | null>(null);
	let cursor = $state<string | null>(null);
	let error = $state<unknown>(null);
	let form = $state({ provider_type: 'individual', display_name: '', claim_channel: 'sms', claim_destination: '' });
	let errors = $state<Record<string, string>>({});
	let showNew = $state(false);

	async function load(c: string | null = null) {
		const p = new URLSearchParams({ limit: '50' });
		if (status) p.set('status', status);
		if (q.trim()) p.set('q', q.trim());
		if (c) p.set('cursor', c);
		try {
			data = (await api.get<Page>('/admin/providers?' + p)).data;
			cursor = c;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = null;
		errors = {};
		const body = { ...form };
		if (!body.claim_destination.trim()) Object.assign(body, { claim_channel: '', claim_destination: '' });
		try {
			const v = (await api.post<{ id: string }>('/admin/providers', body)).data;
			goto('/admin/providers/' + v.id);
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.providers')}</h1>
<FormError {error} />

{#if can('providers.create') && can('providers.update_any')}
	{#if !showNew}
		<button class="btn" type="button" onclick={() => (showNew = true)}>{t('app.admin.provider.new_assisted')}</button>
	{:else}
		<form class="card" onsubmit={create} novalidate>
			<h2>{t('app.admin.provider.new_assisted')}</h2>
			<p class="hint">{t('app.admin.provider.assisted_hint')}</p>
			<div class="grid-2">
				<div class="field">
					<label for="n-type">{t('app.provider.type')}</label>
					<select id="n-type" bind:value={form.provider_type}>
						<option value="individual">{t('app.provider.type_individual')}</option>
						<option value="business">{t('app.provider.type_business')}</option>
					</select>
				</div>
				<Field id="n-name" label={t('app.provider.display_name')} bind:value={form.display_name} maxlength={120} error={errors.display_name} />
				<div class="field">
					<label for="n-ch">{t('app.admin.provider.invite_channel')}</label>
					<select id="n-ch" bind:value={form.claim_channel}>
						<option value="sms">{t('app.admin.channel.sms')}</option>
						<option value="email">{t('app.admin.channel.email')}</option>
					</select>
				</div>
				<div class="field">
					<label for="n-dest">{t('app.admin.provider.invite_destination')} <span class="muted optional">({t('app.common.optional')})</span></label>
					<input id="n-dest" bind:value={form.claim_destination} inputmode={form.claim_channel === 'email' ? 'email' : 'tel'} />
					{#if errors.claim_destination}<p class="field-error">{fieldText(errors.claim_destination)}</p>{/if}
				</div>
			</div>
			<div class="row">
				<button class="btn btn-primary" type="submit" disabled={!form.display_name.trim()}>{t('app.common.create')}</button>
				<button class="btn" type="button" onclick={() => (showNew = false)}>{t('app.common.cancel')}</button>
			</div>
		</form>
	{/if}
{/if}

<nav class="subnav" aria-label={t('app.common.status')}>
	{#each statuses as s (s)}
		<button type="button" class="btn-link" aria-current={status === s ? 'page' : undefined} onclick={() => { status = s; load(); }}>
			{t('app.status.' + s)} ({data?.counts[s] ?? 0})</button>
	{/each}
	<button type="button" class="btn-link" aria-current={status === '' ? 'page' : undefined} onclick={() => { status = ''; load(); }}>{t('app.common.filter_all')}</button>
</nav>
<form class="toolbar" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={q} />
	<button class="btn" type="submit">{t('app.common.search')}</button>
</form>
{#if status === 'pending_review'}<p class="hint">{t('app.admin.provider.queue_hint')}</p>{/if}
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.common.name')}</th><th>{t('app.admin.area')}</th><th>{t('app.admin.provider.completeness')}</th>
			<th>{t('app.common.status')}</th><th>{t('app.admin.provider.submitted')}</th></tr></thead>
		<tbody>
			{#each data?.items ?? [] as p (p.id)}
				<tr>
					<td><a href={'/admin/providers/' + p.id}>{p.display_name}</a>
						<span class="muted">· {t('app.provider.type_' + p.provider_type)}</span>
						{#if p.assisted}<span class="badge status-draft">{p.claimed ? t('app.admin.provider.claimed') : t('app.admin.provider.unclaimed')}</span>{/if}</td>
					<td>{p.base_area ?? '—'}</td>
					<td>{p.completeness}%</td>
					<td><StatusBadge status={p.status} /></td>
					<td class="muted">{p.submitted_at ? formatDate(p.submitted_at) : '—'}</td>
				</tr>
			{:else}<tr><td colspan="5" class="muted">{t('app.admin.empty')}</td></tr>{/each}
		</tbody>
	</table>
</div>
<p class="row">
	{#if cursor}<button class="btn" type="button" onclick={() => load(null)}>{t('app.common.first_page')}</button>{/if}
	{#if data?.next_cursor}<button class="btn" type="button" onclick={() => load(data?.next_cursor ?? null)}>{t('app.common.next_page')}</button>{/if}
</p>
