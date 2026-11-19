<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Flag {
		key: string;
		enabled: boolean;
		description: string;
	}
	let flags = $state<Flag[]>([]);
	let maintenance = $state(false);
	let error = $state<unknown>(null);

	$effect(() => {
		api.get<Flag[]>('/admin/feature-flags').then((r) => (flags = r.data)).catch((e) => (error = e));
		if (can('platform.settings.view')) api.get<{ enabled: boolean }>('/admin/maintenance').then((r) => (maintenance = r.data.enabled)).catch(() => {});
	});

	async function toggle(f: Flag, on: boolean) {
		error = null;
		try {
			flags = (await api.patch<Flag[]>('/admin/feature-flags', { [f.key]: on })).data;
		} catch (err) {
			error = err;
			flags = [...flags];
		}
	}
	async function setMaintenance(on: boolean) {
		if (on && !confirm(t('app.admin.maintenance_confirm'))) return;
		error = null;
		try {
			maintenance = (await api.put<{ enabled: boolean }>('/admin/maintenance', { enabled: on })).data.enabled;
		} catch (err) {
			error = err;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.flags')}</h1>
<FormError {error} />
<section class="card">
	<h2>{t('app.admin.maintenance_title')}</h2>
	<p class={maintenance ? 'field-error' : 'muted'} role="status">{maintenance ? t('app.admin.maintenance_on') : t('app.admin.maintenance_off')}</p>
	{#if maintenance && can('platform.maintenance.disable')}
		<button class="btn btn-primary" type="button" onclick={() => setMaintenance(false)}>{t('app.admin.maintenance_disable')}</button>
	{:else if !maintenance && can('platform.maintenance.enable')}
		<button class="btn btn-danger" type="button" onclick={() => setMaintenance(true)}>{t('app.admin.maintenance_enable')}</button>
	{/if}
</section>
<section class="card">
	<h2>{t('app.admin.flags_title')}</h2>
	<ul class="list-plain">
		{#each flags as f (f.key)}
			<li>
				<label class="check">
					<input type="checkbox" checked={f.enabled} disabled={!can('platform.feature_flags.update')} onchange={(e) => toggle(f, e.currentTarget.checked)} />
					<span><code>{f.key}</code> — {f.description}</span>
				</label>
			</li>
		{/each}
	</ul>
</section>
