<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Row {
		public_id: string;
		display_name: string;
		email: string | null;
		phone: string | null;
		status: string;
		roles: string[];
		created_at: string;
	}

	let q = $state('');
	let status = $state('');
	let rows = $state<Row[]>([]);
	let cursor = $state<string | null>(null);
	let next = $state<string | null>(null);
	let error = $state<unknown>(null);

	async function load(c: string | null = null) {
		error = null;
		const params = new URLSearchParams({ limit: '25' });
		if (q.trim()) params.set('q', q.trim());
		if (status) params.set('status', status);
		if (c) params.set('cursor', c);
		try {
			const res = await api.get<Row[]>('/admin/users?' + params);
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

<h1>{t('app.admin.users')}</h1>
<form class="row" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<input type="search" aria-label={t('app.admin.search_users')} placeholder={t('app.admin.search_users')} bind:value={q} />
	<select aria-label={t('app.admin.status')} bind:value={status} onchange={() => load()}>
		<option value="">{t('app.admin.all_statuses')}</option>
		{#each ['active', 'suspended', 'pending_deletion', 'deleted'] as s (s)}
			<option value={s}>{t('app.admin.status_values.' + s)}</option>
		{/each}
	</select>
</form>
<FormError {error} />
<div class="table-wrap">
	<table>
		<thead>
			<tr><th>{t('app.admin.col_name')}</th><th>{t('app.admin.col_contact')}</th><th>{t('app.admin.col_roles')}</th><th>{t('app.admin.col_status')}</th><th>{t('app.admin.col_created')}</th></tr>
		</thead>
		<tbody>
			{#each rows as u (u.public_id)}
				<tr>
					<td><a href={'/admin/users/' + u.public_id}>{u.display_name}</a></td>
					<td>{u.phone ?? ''}{#if u.phone && u.email}<br />{/if}{u.email ?? ''}</td>
					<td>{#each u.roles as r (r)}<span class="badge">{r}</span>{/each}</td>
					<td>{t('app.admin.status_values.' + u.status)}</td>
					<td>{formatDate(u.created_at)}</td>
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
