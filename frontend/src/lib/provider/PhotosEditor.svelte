<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import type { EditorTarget, Listing, MediaItem, PortfolioItem } from './types.ts';

	let { target, listing, onsaved, maxItems = 30 }: { target: EditorTarget; listing: Listing; onsaved: (l: Listing) => void; maxItems?: number } = $props();

	let error = $state<unknown>(null);
	let fieldErr = $state('');
	let busy = $state('');
	let caption = $state('');
	let items = $state<PortfolioItem[]>([]);
	let dirty = $state(false);

	$effect(() => {
		items = listing.portfolio.map((p) => ({ ...p }));
		dirty = false;
	});

	const thumb = (m: MediaItem | null, w = 480) => (m?.urls?.[String(w)] ?? m?.url ?? '');

	async function upload(purpose: string, input: HTMLInputElement) {
		const file = input.files?.[0];
		if (!file) return;
		busy = purpose;
		error = null;
		fieldErr = '';
		const f = new FormData();
		f.set('purpose', purpose);
		f.set('file', file);
		if (purpose === 'portfolio') f.set('caption', caption);
		try {
			onsaved((await api.upload<Listing>(target.base + '/media', f)).data);
			caption = '';
		} catch (err) {
			error = err;
			if (err instanceof ApiError) fieldErr = err.fields.file ?? '';
		} finally {
			busy = '';
			input.value = '';
		}
	}
	async function remove(m: MediaItem) {
		if (!confirm(t('app.provider.delete_photo_confirm'))) return;
		error = null;
		try {
			onsaved((await api.del<Listing>(target.base + '/media/' + m.id)).data);
		} catch (err) {
			error = err;
		}
	}
	function move(i: number, d: number) {
		const j = i + d;
		if (j < 0 || j >= items.length) return;
		const copy = [...items];
		[copy[i], copy[j]] = [copy[j], copy[i]];
		items = copy;
		dirty = true;
	}
	async function saveOrder() {
		error = null;
		try {
			onsaved((await api.patch<Listing>(target.base + '/portfolio', { items: items.map((p) => ({ id: p.id, caption: p.caption ?? '' })) })).data);
		} catch (err) {
			error = err;
		}
	}
</script>

{#snippet status(m: MediaItem)}
	{#if m.status === 'pending_moderation'}<span class="badge status-draft">{t('app.provider.media_pending')}</span>
	{:else if m.status === 'rejected'}<span class="badge status-archived">{t('app.provider.media_rejected')}</span>{#if m.moderation_note}<span class="hint"> {m.moderation_note}</span>{/if}{/if}
{/snippet}

<FormError {error} />
{#if fieldErr}<p class="field-error" role="alert">{fieldText(fieldErr)}</p>{/if}
<p class="hint">{t('app.provider.photo_rules')}</p>

<div class="grid-2">
	{#each [{ p: 'profile', m: listing.profile_photo }, { p: 'cover', m: listing.cover_photo }] as slot (slot.p)}
		<section class="card">
			<h2>{t('app.provider.photo.' + slot.p)}</h2>
			{#if slot.m}
				<img class={slot.p === 'profile' ? 'avatar' : 'cover'} src={thumb(slot.m)} alt={t('app.provider.photo.' + slot.p)} />
				{@render status(slot.m)}
			{:else}<p class="muted">{t('app.provider.no_photo')}</p>{/if}
			<div class="row">
				<label class="btn">
					{busy === slot.p ? t('app.provider.uploading') : slot.m ? t('app.provider.replace_photo') : t('app.provider.upload_photo')}
					<input class="sr-only" type="file" accept="image/jpeg,image/png,image/webp" disabled={!!busy} onchange={(e) => upload(slot.p, e.currentTarget)} />
				</label>
				{#if slot.m}<button class="btn btn-danger" type="button" onclick={() => slot.m && remove(slot.m)}>{t('app.common.remove')}</button>{/if}
			</div>
		</section>
	{/each}
</div>

<section class="card">
	<h2>{t('app.provider.portfolio', { n: items.length, max: maxItems })}</h2>
	<ul class="gallery">
		{#each items as it, i (it.id)}
			<li>
				<img src={thumb(it.media)} alt={it.caption ?? ''} loading="lazy" />
				{@render status(it.media)}
				<input aria-label={t('app.provider.caption')} placeholder={t('app.provider.caption')} maxlength="200" bind:value={it.caption} oninput={() => (dirty = true)} />
				<div class="row">
					<button class="btn" type="button" aria-label={t('app.common.move_up')} onclick={() => move(i, -1)} disabled={i === 0}>↑</button>
					<button class="btn" type="button" aria-label={t('app.common.move_down')} onclick={() => move(i, 1)} disabled={i === items.length - 1}>↓</button>
					<button class="btn btn-danger" type="button" onclick={() => remove(it.media)}>{t('app.common.remove')}</button>
				</div>
			</li>
		{:else}<li class="muted">{t('app.provider.no_portfolio')}</li>{/each}
	</ul>
	{#if dirty}<button class="btn btn-primary" type="button" onclick={saveOrder}>{t('app.provider.save_order')}</button>{/if}
	{#if items.length < maxItems}
		<div class="field">
			<label for="pf-cap">{t('app.provider.caption')} <span class="muted optional">({t('app.common.optional')})</span></label>
			<input id="pf-cap" maxlength="200" bind:value={caption} />
		</div>
		<label class="btn btn-primary">
			{busy === 'portfolio' ? t('app.provider.uploading') : t('app.provider.add_photo')}
			<input class="sr-only" type="file" accept="image/jpeg,image/png,image/webp" disabled={!!busy} onchange={(e) => upload('portfolio', e.currentTarget)} />
		</label>
	{/if}
</section>

<style>
	.avatar { width: 120px; height: 120px; object-fit: cover; border-radius: 50%; display: block; }
	.cover { width: 100%; max-height: 160px; object-fit: cover; display: block; border-radius: 6px; }
	.gallery { list-style: none; padding: 0; display: grid; gap: 0.75rem; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); }
	.gallery img { width: 100%; aspect-ratio: 4 / 3; object-fit: cover; border-radius: 6px; display: block; }
	.gallery input { width: 100%; margin: 0.35rem 0; }
	label.btn { cursor: pointer; }
</style>
