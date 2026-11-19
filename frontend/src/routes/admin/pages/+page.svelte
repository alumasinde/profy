<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Page {
		id: string;
		title: string;
		path: string;
		status: string;
		updated_at: string;
	}
	let pages = $state<Page[]>([]);
	let status = $state('');
	let title = $state('');
	let error = $state<unknown>(null);
	let fieldErrors = $state<Record<string, string>>({});

	async function load() {
		try {
			pages = (await api.get<Page[]>('/admin/pages' + (status ? '?status=' + status : ''))).data;
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
		fieldErrors = {};
		try {
			const p = (await api.post<Page>('/admin/pages', { title })).data;
			goto('/admin/pages/' + p.id);
		} catch (err) {
			error = err;
			if (err instanceof ApiError) fieldErrors = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.pages')}</h1>
<FormError {error} />
{#if can('pages.create')}
	<form class="card" onsubmit={create} novalidate>
		<h2>{t('app.admin.new_page')}</h2>
		<Field id="new-title" label={t('app.admin.page_title')} bind:value={title} error={fieldErrors.title ?? fieldErrors.slug} />
		<button class="btn btn-primary" type="submit" disabled={!title.trim()}>{t('app.common.create')}</button>
	</form>
{/if}
<div class="toolbar">
	<select aria-label={t('app.common.status')} bind:value={status} onchange={load}>
		<option value="">{t('app.common.filter_all')}</option>
		{#each ['draft', 'published', 'archived'] as s (s)}<option value={s}>{t('app.status.' + s)}</option>{/each}
	</select>
</div>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.page_title')}</th><th>{t('app.admin.page_path')}</th><th>{t('app.common.status')}</th><th>{t('app.admin.col_when')}</th></tr></thead>
		<tbody>
			{#each pages as p (p.id)}
				<tr>
					<td><a href={'/admin/pages/' + p.id}>{p.title}</a></td>
					<td><code>{p.path}</code></td>
					<td><StatusBadge status={p.status} /></td>
					<td>{formatDate(p.updated_at)}</td>
				</tr>
			{:else}
				<tr><td colspan="4" class="muted">{t('app.admin.empty')}</td></tr>
			{/each}
		</tbody>
	</table>
</div>
