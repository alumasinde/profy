<script lang="ts">
	import { page } from '$app/state';
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface User {
		public_id: string;
		first_name: string;
		middle_name: string | null;
		last_name: string;
		display_name: string;
		email: string | null;
		phone: string | null;
		status: string;
		roles: string[];
		created_at: string;
		last_login_at: string | null;
	}
	interface Role {
		key: string;
		name: string;
		is_protected: boolean;
	}

	let user = $state<User | null>(null);
	let roles = $state<Role[]>([]);
	let selected = $state<string[]>([]);
	let reason = $state('');
	let error = $state<unknown>(null);
	let saved = $state(false);

	const id = $derived(page.params.id ?? '');

	async function load() {
		try {
			user = (await api.get<User>('/admin/users/' + encodeURIComponent(id))).data;
			selected = [...user.roles];
			if (can('roles.view')) roles = (await api.get<Role[]>('/admin/roles')).data;
		} catch (err) {
			error = err;
		}
	}
	$effect(() => {
		load();
	});

	async function saveRoles() {
		error = null;
		saved = false;
		try {
			user = (await api.put<User>('/admin/users/' + encodeURIComponent(id) + '/roles', { roles: selected })).data;
			saved = true;
		} catch (err) {
			error = err;
		}
	}

	async function setStatus(status: string) {
		error = null;
		try {
			user = (await api.patch<User>('/admin/users/' + encodeURIComponent(id) + '/status', { status, reason })).data;
			reason = '';
		} catch (err) {
			error = err;
		}
	}
</script>

<p><a href="/admin/users">← {t('app.admin.users')}</a></p>
<FormError {error} />
{#if user}
	<h1>{user.display_name}</h1>
	<section class="card">
		<h2>{t('app.admin.user_detail')}</h2>
		<dl>
			<dt>{t('app.fields.first_name')}</dt><dd>{user.first_name}</dd>
			<dt>{t('app.fields.middle_name')}</dt><dd>{user.middle_name ?? t('app.common.none')}</dd>
			<dt>{t('app.fields.last_name')}</dt><dd>{user.last_name}</dd>
			<dt>{t('app.admin.col_contact')}</dt><dd>{user.phone ?? ''} {user.email ?? ''}</dd>
			<dt>{t('app.admin.col_status')}</dt><dd>{t('app.admin.status_values.' + user.status)}</dd>
			<dt>{t('app.admin.col_created')}</dt><dd>{formatDate(user.created_at)}</dd>
		</dl>
	</section>

	{#if can('users.roles.assign') && roles.length}
		<section class="card">
			<h2>{t('app.admin.roles_title')}</h2>
			{#each roles as r (r.key)}
				<label class="checkbox">
					<input type="checkbox" value={r.key} bind:group={selected} />
					<span>{r.name}</span>
				</label>
			{/each}
			{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
			<button class="btn btn-primary" type="button" onclick={saveRoles}>{t('app.admin.save_roles')}</button>
		</section>
	{/if}

	{#if user.status === 'active' && can('users.suspend')}
		<section class="card">
			<div class="field">
				<label for="reason">{t('app.admin.suspend_reason')}</label>
				<textarea id="reason" rows="2" bind:value={reason}></textarea>
			</div>
			<button class="btn btn-danger" type="button" disabled={!reason.trim()} onclick={() => setStatus('suspended')}>{t('app.admin.suspend')}</button>
		</section>
	{:else if user.status === 'suspended' && can('users.restore')}
		<section class="card">
			<button class="btn" type="button" onclick={() => setStatus('active')}>{t('app.admin.restore')}</button>
		</section>
	{/if}
{/if}

<style>
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.25rem 1rem;
	}
	dt {
		font-weight: 600;
	}
	dd {
		margin: 0;
	}
</style>
