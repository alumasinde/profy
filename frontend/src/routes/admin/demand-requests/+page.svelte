<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Req {
		id: string; service_text: string; location_text: string; notes?: string; contact_channel?: string; contact_value?: string;
		contact_purged: boolean; status: string; staff_note?: string; created_at: string;
	}
	const statuses = ['new', 'in_progress', 'fulfilled', 'closed'];
	let status = $state('new');
	let rows = $state<Req[]>([]);
	let next = $state<string | null>(null);
	let cursor = $state<string | null>(null);
	let error = $state<unknown>(null);
	let notes = $state<Record<string, string>>({});

	async function load(c: string | null = null) {
		const p = new URLSearchParams({ limit: '50' });
		if (status) p.set('status', status);
		if (c) p.set('cursor', c);
		try {
			const res = await api.get<Req[]>('/admin/demand-requests?' + p);
			rows = res.data;
			cursor = c;
			next = (res.meta?.next_cursor as string) || null;
			notes = Object.fromEntries(rows.map((r) => [r.id, r.staff_note ?? '']));
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});
	async function setStatus(r: Req, s: string) {
		error = null;
		try {
			await api.patch('/admin/demand-requests/' + r.id, { status: s, staff_note: notes[r.id] ?? '' });
			r.status = s;
			if (status && s !== status) rows = rows.filter((x) => x.id !== r.id);
		} catch (e) {
			error = e;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.demand')}</h1>
<p class="lead">{t('app.admin.discovery.demand_lead')}</p>
<FormError {error} />
<nav class="subnav" aria-label={t('app.common.status')}>
	{#each statuses as s (s)}
		<button type="button" class="btn-link" aria-current={status === s ? 'page' : undefined} onclick={() => { status = s; load(); }}>{t('app.admin.discovery.demand_status.' + s)}</button>
	{/each}
	<button type="button" class="btn-link" aria-current={status === '' ? 'page' : undefined} onclick={() => { status = ''; load(); }}>{t('app.common.filter_all')}</button>
</nav>
<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.discovery.need')}</th><th>{t('app.admin.discovery.contact')}</th><th>{t('app.admin.discovery.received')}</th><th>{t('app.common.status')}</th></tr></thead>
		<tbody>
			{#each rows as r (r.id)}
				<tr>
					<td><strong>{r.service_text || '—'}</strong>{#if r.location_text}<br /><span class="muted">{r.location_text}</span>{/if}
						{#if r.notes}<p class="hint">{r.notes}</p>{/if}</td>
					<td>{#if r.contact_value}{t('app.admin.channel.' + r.contact_channel)}: {r.contact_value}{:else if r.contact_purged}<span class="muted">{t('app.admin.discovery.contact_purged')}</span>{:else}<span class="muted">—</span>{/if}</td>
					<td class="muted">{formatDate(r.created_at)}</td>
					<td>
						<StatusBadge status={r.status} />
						{#if can('demand_requests.update')}
							<input aria-label={t('app.admin.discovery.staff_note')} placeholder={t('app.admin.discovery.staff_note')} maxlength="500" bind:value={notes[r.id]} />
							<div class="row">
								{#each statuses.filter((s) => s !== r.status) as s (s)}
									<button class="btn" type="button" onclick={() => setStatus(r, s)}>{t('app.admin.discovery.demand_status.' + s)}</button>
								{/each}
							</div>
						{:else if r.staff_note}<p class="hint">{r.staff_note}</p>{/if}
					</td>
				</tr>
			{:else}<tr><td colspan="4" class="muted">{t('app.admin.empty')}</td></tr>{/each}
		</tbody>
	</table>
</div>
<p class="row">
	{#if cursor}<button class="btn" type="button" onclick={() => load(null)}>{t('app.common.first_page')}</button>{/if}
	{#if next}<button class="btn" type="button" onclick={() => load(next)}>{t('app.common.next_page')}</button>{/if}
</p>
