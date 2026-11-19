<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Gap {
		service?: { slug: string; name: string; path: string }; query?: string;
		location?: { slug: string; label: string }; location_slug?: string; searches: number; requests: number; last_at: string;
	}
	let days = $state(30);
	let rows = $state<Gap[]>([]);
	let error = $state<unknown>(null);
	async function load() {
		try {
			rows = (await api.get<Gap[]>('/admin/search-gaps?days=' + days)).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.search_gaps')}</h1>
<p class="lead">{t('app.admin.discovery.gaps_lead')}</p>
<FormError {error} />
<div class="toolbar">
	<label for="g-days">{t('app.admin.discovery.period')}</label>
	<select id="g-days" bind:value={days} onchange={load}>
		{#each [7, 30, 90] as d (d)}<option value={d}>{t('app.admin.discovery.last_days', { n: d })}</option>{/each}
	</select>
</div>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.discovery.searched_for')}</th><th>{t('app.admin.discovery.where')}</th><th>{t('app.admin.discovery.searches')}</th>
			<th>{t('app.admin.discovery.requests')}</th><th>{t('app.admin.discovery.last')}</th></tr></thead>
		<tbody>
			{#each rows as g, i (i)}
				<tr>
					<td>{#if g.service}{g.service.name}{:else if g.query}“{g.query}” <span class="badge status-draft">{t('app.admin.discovery.unmatched')}</span>{:else}<span class="muted">{t('app.admin.discovery.any_service')}</span>{/if}</td>
					<td>{g.location?.label ?? (g.location_slug === 'near_me' ? t('app.admin.discovery.near_me') : g.location_slug || '—')}</td>
					<td>{g.searches}</td>
					<td>{g.requests}</td>
					<td class="muted">{formatDate(g.last_at)}</td>
				</tr>
			{:else}<tr><td colspan="5" class="muted">{t('app.admin.empty')}</td></tr>{/each}
		</tbody>
	</table>
</div>
<p class="hint">{t('app.admin.discovery.gaps_hint')}</p>
