<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Entry {
		id: string;
		actor_name: string | null;
		action: string;
		entity_type: string;
		entity_id: string | null;
		old_values?: unknown;
		new_values?: unknown;
		ip_address?: string;
		created_at: string;
	}

	let action = $state('');
	let rows = $state<Entry[]>([]);
	let cursor = $state<string | null>(null);
	let next = $state<string | null>(null);
	let error = $state<unknown>(null);

	async function load(c: string | null = null) {
		const params = new URLSearchParams({ limit: '50' });
		if (action.trim()) params.set('action', action.trim());
		if (c) params.set('cursor', c);
		try {
			const res = await api.get<Entry[]>('/admin/audit-logs?' + params);
			rows = res.data;
			cursor = c;
			next = (res.meta?.next_cursor as string | null) ?? null;
		} catch (err) {
			error = err;
		}
	}
	$effect(() => {
		load();
	});
</script>

<h1>{t('app.admin.audit')}</h1>
<form class="row" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<input type="search" aria-label={t('app.admin.filter_action')} placeholder={t('app.admin.filter_action')} bind:value={action} />
</form>
<FormError {error} />
<div class="table-wrap">
	<table>
		<thead>
			<tr><th>{t('app.admin.col_when')}</th><th>{t('app.admin.col_actor')}</th><th>{t('app.admin.col_action')}</th><th>{t('app.admin.col_entity')}</th><th>{t('app.admin.col_changes')}</th></tr>
		</thead>
		<tbody>
			{#each rows as e (e.id)}
				<tr>
					<td>{formatDate(e.created_at)}{#if e.ip_address}<br /><span class="muted">{e.ip_address}</span>{/if}</td>
					<td>{e.actor_name ?? t('app.admin.system_actor')}</td>
					<td><code>{e.action}</code></td>
					<td>{e.entity_type}<br /><span class="muted">{e.entity_id ?? ''}</span></td>
					<td>
						{#if e.old_values}<pre class="json">− {JSON.stringify(e.old_values)}</pre>{/if}
						{#if e.new_values}<pre class="json">+ {JSON.stringify(e.new_values)}</pre>{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="5" class="muted">{t('app.admin.empty')}</td></tr>
			{/each}
		</tbody>
	</table>
</div>
<p class="row">
	{#if cursor}<button class="btn" type="button" onclick={() => load(null)}>{t('app.common.first_page')}</button>{/if}
	{#if next}<button class="btn" type="button" onclick={() => load(next)}>{t('app.common.next_page')}</button>{/if}
</p>
