<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { t } from '#lib/i18n.svelte.ts';

	interface County {
		id: string;
		name: string;
		code: string;
		is_selectable: boolean;
		is_indexable: boolean;
		child_count: number;
	}
	let counties = $state<County[]>([]);
	let q = $state('');
	let error = $state<unknown>(null);
	const shown = $derived(counties.filter((c) => c.name.toLowerCase().includes(q.trim().toLowerCase())));

	$effect(() => {
		api.get<County[]>('/admin/locations/counties?limit=100').then((r) => (counties = r.data)).catch((e) => (error = e));
	});
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.locations')}</h1>
<FormError {error} />
<div class="toolbar">
	<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={q} />
</div>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.county')}</th><th>#</th><th>{t('app.admin.towns')}</th><th>{t('app.admin.selectable')}</th><th>{t('app.admin.indexable')}</th></tr></thead>
		<tbody>
			{#each shown as c (c.id)}
				<tr>
					<td><a href={'/admin/locations/counties/' + c.id}>{c.name}</a></td>
					<td>{c.code}</td>
					<td>{c.child_count}</td>
					<td>{c.is_selectable ? t('app.common.yes') : t('app.common.no')}</td>
					<td>{c.is_indexable ? t('app.common.yes') : t('app.common.no')}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
