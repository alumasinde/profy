<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Redirect {
		id: string;
		old_path: string;
		new_path: string;
		source: string;
		created_at: string;
	}
	let rows = $state<Redirect[]>([]);
	let q = $state('');
	let next = $state<string | null>(null);
	let form = $state({ old_path: '', new_path: '' });
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);

	async function load(cursor: string | null = null) {
		const params = new URLSearchParams({ limit: '50' });
		if (q.trim()) params.set('q', q.trim());
		if (cursor) params.set('cursor', cursor);
		try {
			const res = await api.get<Redirect[]>('/admin/redirects?' + params);
			rows = cursor ? [...rows, ...res.data] : res.data;
			next = (res.meta?.next_cursor as string | null) ?? null;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});

	async function add(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		error = null;
		try {
			await api.post('/admin/redirects', form);
			form = { old_path: '', new_path: '' };
			await load();
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		}
	}
	async function remove(id: string) {
		if (!confirm(t('app.common.confirm'))) return;
		await api.del('/admin/redirects/' + id).catch((e) => (error = e));
		await load();
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.redirects')}</h1>
<FormError {error} />
<form class="card" onsubmit={add} novalidate>
	<div class="grid-2">
		<Field id="old" label={t('app.admin.redirect_from')} placeholder="/old-path" bind:value={form.old_path} error={errors.old_path} />
		<Field id="new" label={t('app.admin.redirect_to')} placeholder="/new-path" bind:value={form.new_path} error={errors.new_path} />
	</div>
	<button class="btn btn-primary" type="submit" disabled={!form.old_path || !form.new_path}>{t('app.admin.add_redirect')}</button>
</form>
<form class="toolbar" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={q} />
</form>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.redirect_from')}</th><th>{t('app.admin.redirect_to')}</th><th>{t('app.common.status')}</th><th>{t('app.admin.col_when')}</th><th></th></tr></thead>
		<tbody>
			{#each rows as r (r.id)}
				<tr>
					<td><code>{r.old_path}</code></td>
					<td><code>{r.new_path}</code></td>
					<td>{t('app.admin.redirect_source.' + r.source)}</td>
					<td>{formatDate(r.created_at)}</td>
					<td><button class="btn btn-danger" type="button" onclick={() => remove(r.id)}>{t('app.common.remove')}</button></td>
				</tr>
			{:else}
				<tr><td colspan="5" class="muted">{t('app.admin.empty')}</td></tr>
			{/each}
		</tbody>
	</table>
</div>
{#if next}<button class="btn" type="button" onclick={() => load(next)}>{t('app.common.loading_more')}</button>{/if}
