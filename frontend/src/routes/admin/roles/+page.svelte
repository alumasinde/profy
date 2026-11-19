<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Role {
		key: string;
		name: string;
		description: string;
		is_protected: boolean;
		user_count: number;
		permissions: string[];
	}
	interface Perm {
		key: string;
		module: string;
	}

	let roles = $state<Role[]>([]);
	let perms = $state<Perm[]>([]);
	let editing = $state<string | null>(null);
	let selected = $state<string[]>([]);
	let error = $state<unknown>(null);

	const modules = $derived([...new Set(perms.map((p) => p.module))]);

	async function load() {
		try {
			roles = (await api.get<Role[]>('/admin/roles')).data;
			if (can('permissions.view')) perms = (await api.get<Perm[]>('/admin/permissions')).data;
		} catch (err) {
			error = err;
		}
	}
	$effect(() => {
		load();
	});

	function edit(r: Role) {
		editing = r.key;
		selected = [...r.permissions];
	}

	async function save() {
		error = null;
		try {
			await api.put('/admin/roles/' + encodeURIComponent(editing!) + '/permissions', { permissions: selected });
			editing = null;
			await load();
		} catch (err) {
			error = err;
		}
	}
</script>

<h1>{t('app.admin.roles')}</h1>
<FormError {error} />
{#each roles as r (r.key)}
	<section class="card">
		<h2>{r.name}</h2>
		<p class="muted">{r.description}</p>
		<p>
			<span class="badge">{t('app.admin.permissions_count', { count: r.permissions.length })}</span>
			<span class="badge">{t('app.admin.users_count', { count: r.user_count })}</span>
		</p>
		{#if r.is_protected}
			<p class="muted">{t('app.admin.protected_role')}</p>
		{:else if editing === r.key}
			{#each modules as m (m)}
				<h3>{m}</h3>
				<div class="perm-grid">
					{#each perms.filter((p) => p.module === m) as p (p.key)}
						<label><input type="checkbox" value={p.key} bind:group={selected} /> <code>{p.key}</code></label>
					{/each}
				</div>
			{/each}
			<div class="row">
				<button class="btn btn-primary" type="button" onclick={save}>{t('app.admin.save_permissions')}</button>
				<button class="btn" type="button" onclick={() => (editing = null)}>{t('app.common.cancel')}</button>
			</div>
		{:else if can('roles.permissions.assign') && perms.length}
			<button class="btn" type="button" onclick={() => edit(r)}>{t('app.admin.edit_permissions')}</button>
		{/if}
	</section>
{/each}
