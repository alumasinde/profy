<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Category {
		id: string;
		parent_id: string | null;
		name: string;
		status: string;
	}
	interface Item {
		id: string;
		name: string;
		path: string;
		category_name: string;
		status: string;
		requires_licence: boolean;
	}
	let cats = $state<Category[]>([]);
	let rows = $state<Item[]>([]);
	let q = $state('');
	let category = $state('');
	let status = $state('');
	let cursor = $state<string | null>(null);
	let next = $state<string | null>(null);
	let error = $state<unknown>(null);
	let createForm = $state({ name: '', category_id: '' });
	let createErrors = $state<Record<string, string>>({});

	const catLabel = (c: Category) => {
		const p = c.parent_id ? cats.find((x) => x.id === c.parent_id) : null;
		return p ? p.name + ' › ' + c.name : c.name;
	};

	async function load(c: string | null = null) {
		const params = new URLSearchParams({ limit: '50' });
		if (q.trim()) params.set('q', q.trim());
		if (category) params.set('category_id', category);
		if (status) params.set('status', status);
		if (c) params.set('cursor', c);
		try {
			const res = await api.get<Item[]>('/admin/services?' + params);
			rows = res.data;
			cursor = c;
			next = (res.meta?.next_cursor as string | null) ?? null;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		api.get<Category[]>('/admin/categories').then((r) => (cats = r.data)).catch(() => {});
		load();
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		createErrors = {};
		error = null;
		try {
			const it = (await api.post<Item>('/admin/services', createForm)).data;
			goto('/admin/services/' + it.id);
		} catch (err) {
			error = err;
			if (err instanceof ApiError) createErrors = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.services')}</h1>
<FormError {error} />
{#if can('services.create')}
	<form class="card" onsubmit={create} novalidate>
		<h2>{t('app.admin.new_service')}</h2>
		<div class="grid-2">
			<Field id="s-name" label={t('app.common.name')} bind:value={createForm.name} error={createErrors.name ?? createErrors.slug} />
			<div class="field">
				<label for="s-cat">{t('app.admin.category')}</label>
				<select id="s-cat" bind:value={createForm.category_id}>
					<option value="" disabled>—</option>
					{#each cats.filter((c) => c.status !== 'archived') as c (c.id)}<option value={c.id}>{catLabel(c)}</option>{/each}
				</select>
				{#if createErrors.category_id}<p class="field-error">{t('fields.' + createErrors.category_id)}</p>{/if}
			</div>
		</div>
		<button class="btn btn-primary" type="submit" disabled={!createForm.name.trim() || !createForm.category_id}>{t('app.common.create')}</button>
	</form>
{/if}
<form class="toolbar" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={q} />
	<select aria-label={t('app.admin.category')} bind:value={category} onchange={() => load()}>
		<option value="">{t('app.common.filter_all')}</option>
		{#each cats as c (c.id)}<option value={c.id}>{catLabel(c)}</option>{/each}
	</select>
	<select aria-label={t('app.common.status')} bind:value={status} onchange={() => load()}>
		<option value="">{t('app.common.filter_all')}</option>
		{#each ['draft', 'published', 'archived'] as s (s)}<option value={s}>{t('app.status.' + s)}</option>{/each}
	</select>
</form>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.common.name')}</th><th>{t('app.admin.category')}</th><th>{t('app.admin.page_path')}</th><th>{t('app.common.status')}</th></tr></thead>
		<tbody>
			{#each rows as s (s.id)}
				<tr>
					<td><a href={'/admin/services/' + s.id}>{s.name}</a></td>
					<td>{s.category_name}</td>
					<td><code>{s.path}</code></td>
					<td><StatusBadge status={s.status} /></td>
				</tr>
			{:else}
				<tr><td colspan="4" class="muted">{t('app.admin.empty')}</td></tr>
			{/each}
		</tbody>
	</table>
</div>
<p class="row">
	{#if cursor}<button class="btn" type="button" onclick={() => load(null)}>{t('app.common.first_page')}</button>{/if}
	{#if next}<button class="btn" type="button" onclick={() => load(next)}>{t('app.common.next_page')}</button>{/if}
</p>
