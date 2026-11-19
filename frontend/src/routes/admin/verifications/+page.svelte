<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Rec { id: string; label: string; service_name?: string; status: string; submitted_at: string | null; provider?: { id: string; display_name: string; owner_name: string } }
	let status = $state('pending');
	let rows = $state<Rec[]>([]);
	let error = $state<unknown>(null);

	async function load() {
		try {
			rows = (await api.get<Rec[]>('/admin/verifications?status=' + status)).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.verifications')}</h1>
<FormError {error} />
<nav class="subnav" aria-label={t('app.common.status')}>
	{#each ['pending', 'verified', 'rejected', 'expired'] as s (s)}
		<button type="button" class="btn-link" aria-current={status === s ? 'page' : undefined} onclick={() => { status = s; load(); }}>{t('app.status.' + s)}</button>
	{/each}
</nav>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.provider.provider')}</th><th>{t('app.provider.badge')}</th><th>{t('app.common.status')}</th><th>{t('app.admin.provider.submitted')}</th></tr></thead>
		<tbody>
			{#each rows as r (r.id)}
				<tr>
					<td><a href={'/admin/verifications/' + r.id}>{r.provider?.display_name ?? '—'}</a> <span class="muted">{r.provider?.owner_name ?? ''}</span></td>
					<td>{r.label}{#if r.service_name} — {r.service_name}{/if}</td>
					<td><StatusBadge status={r.status} /></td>
					<td class="muted">{r.submitted_at ? formatDate(r.submitted_at) : '—'}</td>
				</tr>
			{:else}<tr><td colspan="4" class="muted">{t('app.admin.empty')}</td></tr>{/each}
		</tbody>
	</table>
</div>
