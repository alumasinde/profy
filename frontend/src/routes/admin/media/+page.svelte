<script lang="ts">
	import { api } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { formatDate, t } from '#lib/i18n.svelte.ts';

	interface Item { id: string; purpose: string; status: string; url?: string; urls?: Record<string, string>; created_at: string; provider_name: string; provider_id: string; moderation_note?: string | null }
	let status = $state('pending_moderation');
	let rows = $state<Item[]>([]);
	let notes = $state<Record<string, string>>({});
	let error = $state<unknown>(null);

	async function load() {
		try {
			rows = (await api.get<Item[]>('/admin/media?status=' + status)).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});
	async function decide(m: Item, decision: 'approve' | 'reject') {
		error = null;
		try {
			await api.patch('/admin/media/' + m.id, { decision, note: notes[m.id] ?? '' });
			rows = rows.filter((r) => r.id !== m.id);
		} catch (e) {
			error = e;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.media')}</h1>
<FormError {error} />
<nav class="subnav" aria-label={t('app.common.status')}>
	{#each ['pending_moderation', 'active', 'rejected'] as s (s)}
		<button type="button" class="btn-link" aria-current={status === s ? 'page' : undefined} onclick={() => { status = s; load(); }}>{t('app.status.' + s)}</button>
	{/each}
</nav>
<ul class="media-grid">
	{#each rows as m (m.id)}
		<li class="card">
			<img src={m.urls?.['480'] ?? m.url} alt="" loading="lazy" />
			<p><a href={'/admin/providers/' + m.provider_id}>{m.provider_name}</a> · {t('app.provider.photo.' + m.purpose)}</p>
			<p class="muted">{formatDate(m.created_at)}</p>
			{#if m.moderation_note}<p class="hint">{m.moderation_note}</p>{/if}
			<label class="sr-only" for={'n-' + m.id}>{t('app.admin.provider.reason_label')}</label>
			<input id={'n-' + m.id} placeholder={t('app.admin.provider.reject_note')} maxlength="500" bind:value={notes[m.id]} />
			<div class="row">
				{#if m.status !== 'active'}<button class="btn btn-primary" type="button" onclick={() => decide(m, 'approve')}>{t('app.admin.provider.action.approve')}</button>{/if}
				{#if m.status !== 'rejected'}<button class="btn btn-danger" type="button" disabled={!notes[m.id]?.trim()} onclick={() => decide(m, 'reject')}>{t('app.admin.provider.action.reject')}</button>{/if}
			</div>
		</li>
	{:else}<li class="muted">{t('app.admin.empty')}</li>{/each}
</ul>

<style>
	.media-grid { list-style: none; padding: 0; display: grid; gap: 0.75rem; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); }
	.media-grid img { width: 100%; aspect-ratio: 4 / 3; object-fit: cover; border-radius: 6px; display: block; }
	.media-grid input { width: 100%; margin: 0.35rem 0; }
</style>
