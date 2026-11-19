<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import { formatKES, type EditorTarget, type Listing, type Offered } from './types.ts';

	let { target, listing, onsaved }: { target: EditorTarget; listing: Listing; onsaved: (l: Listing) => void } = $props();

	interface ServiceRef { id: string; name: string; category_name: string; requires_licence: boolean }
	interface Draft { custom_title: string; description: string; pricing_type: string; price_from: string; price_to: string; status: string }

	let q = $state('');
	let results = $state<ServiceRef[]>([]);
	let picked = $state<ServiceRef | null>(null);
	let draft = $state<Draft>(blank());
	let editing = $state<string | null>(null);
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let busy = $state(false);

	function blank(): Draft {
		return { custom_title: '', description: '', pricing_type: 'from', price_from: '', price_to: '', status: 'active' };
	}
	const taken = $derived(new Set(listing.services.map((s) => s.service_id)));

	let timer: ReturnType<typeof setTimeout> | undefined;
	function search() {
		clearTimeout(timer);
		timer = setTimeout(async () => {
			try {
				results = (await api.get<ServiceRef[]>('/services?q=' + encodeURIComponent(q.trim()))).data.filter((s) => !taken.has(s.id)).slice(0, 12);
			} catch (e) {
				error = e;
			}
		}, 250);
	}

	function payload(d: Draft) {
		const num = (v: string) => (v.trim() === '' ? null : Number(v.replace(/[,\s]/g, '')));
		return {
			custom_title: d.custom_title, description: d.description, pricing_type: d.pricing_type,
			price_from: d.pricing_type === 'on_quote' ? null : num(d.price_from),
			price_to: d.pricing_type === 'range' ? num(d.price_to) : null, status: d.status
		};
	}

	async function run(fn: () => Promise<{ data: Listing }>) {
		busy = true;
		error = null;
		errors = {};
		try {
			onsaved((await fn()).data);
			return true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
			return false;
		} finally {
			busy = false;
		}
	}

	async function add(e: SubmitEvent) {
		e.preventDefault();
		if (!picked) return;
		const p = picked;
		if (await run(() => api.post<Listing>(target.base + '/services', { service_id: p.id, ...payload(draft) }))) {
			picked = null;
			draft = blank();
			q = '';
			results = [];
		}
	}

	function startEdit(o: Offered) {
		editing = o.id;
		draft = {
			custom_title: o.custom_title ?? '', description: o.description ?? '', pricing_type: o.pricing_type,
			price_from: o.price_from === null ? '' : String(o.price_from), price_to: o.price_to === null ? '' : String(o.price_to), status: o.status
		};
	}
	async function update(e: SubmitEvent, id: string) {
		e.preventDefault();
		if (await run(() => api.patch<Listing>(target.base + '/services/' + id, payload(draft)))) {
			editing = null;
			draft = blank();
		}
	}
	async function remove(o: Offered) {
		if (confirm(t('app.provider.remove_service_confirm', { name: o.service_name }))) await run(() => api.del<Listing>(target.base + '/services/' + o.id));
	}

	function price(o: Offered): string {
		switch (o.pricing_type) {
			case 'fixed': return t('app.provider.price_fixed', { amount: formatKES(o.price_from) });
			case 'from': return t('app.provider.price_from', { amount: formatKES(o.price_from) });
			case 'range': return t('app.provider.price_range', { from: formatKES(o.price_from), to: formatKES(o.price_to) });
		}
		return t('app.provider.price_on_quote');
	}
	const err = (k: string) => (errors[k] ? fieldText(errors[k]) : '');
</script>

{#snippet pricing(prefix: string)}
	<div class="grid-2">
		<div class="field">
			<label for={prefix + '-pt'}>{t('app.provider.pricing_type')}</label>
			<select id={prefix + '-pt'} bind:value={draft.pricing_type}>
				{#each ['fixed', 'from', 'range', 'on_quote'] as p (p)}<option value={p}>{t('app.provider.pricing.' + p)}</option>{/each}
			</select>
		</div>
		<div class="field">
			<label for={prefix + '-st'}>{t('app.common.status')}</label>
			<select id={prefix + '-st'} bind:value={draft.status}>
				<option value="active">{t('app.provider.offer_active')}</option>
				<option value="hidden">{t('app.provider.offer_hidden')}</option>
			</select>
		</div>
		{#if draft.pricing_type !== 'on_quote'}
			<div class="field">
				<label for={prefix + '-pf'}>{draft.pricing_type === 'range' ? t('app.provider.price_min') : t('app.provider.price_kes')}</label>
				<input id={prefix + '-pf'} inputmode="numeric" bind:value={draft.price_from} aria-invalid={errors.price_from ? 'true' : undefined} />
				{#if err('price_from')}<p class="field-error">{err('price_from')}</p>{/if}
			</div>
		{/if}
		{#if draft.pricing_type === 'range'}
			<div class="field">
				<label for={prefix + '-pto'}>{t('app.provider.price_max')}</label>
				<input id={prefix + '-pto'} inputmode="numeric" bind:value={draft.price_to} aria-invalid={errors.price_to ? 'true' : undefined} />
				{#if err('price_to')}<p class="field-error">{err('price_to')}</p>{/if}
			</div>
		{/if}
	</div>
	<div class="field">
		<label for={prefix + '-title'}>{t('app.provider.custom_title')} <span class="muted optional">({t('app.common.optional')})</span></label>
		<input id={prefix + '-title'} maxlength="120" bind:value={draft.custom_title} />
	</div>
	<div class="field">
		<label for={prefix + '-desc'}>{t('app.common.description')} <span class="muted optional">({t('app.common.optional')})</span></label>
		<textarea id={prefix + '-desc'} rows="2" maxlength="500" bind:value={draft.description}></textarea>
	</div>
{/snippet}

<FormError {error} />
<section class="card">
	<h2>{t('app.provider.your_services', { n: listing.services.length })}</h2>
	{#if listing.services.length === 0}<p class="muted">{t('app.provider.no_services')}</p>{/if}
	<ul class="list-plain offers">
		{#each listing.services as o (o.id)}
			<li>
				{#if editing === o.id}
					<form onsubmit={(e) => update(e, o.id)} novalidate>
						<h3>{o.service_name}</h3>
						{@render pricing('e-' + o.id)}
						<div class="row">
							<button class="btn btn-primary" type="submit" disabled={busy}>{t('app.common.save')}</button>
							<button class="btn" type="button" onclick={() => { editing = null; draft = blank(); }}>{t('app.common.cancel')}</button>
						</div>
					</form>
				{:else}
					<div class="offer-row">
						<div>
							<strong>{o.custom_title || o.service_name}</strong>
							<span class="muted"> · {o.category_name}{#if o.custom_title} · {o.service_name}{/if}</span>
							<div>{price(o)}
								{#if o.status === 'hidden'}<span class="badge status-archived">{t('app.provider.offer_hidden')}</span>{/if}
								{#if !o.service_live}<span class="badge status-archived">{t('app.provider.service_unavailable')}</span>{/if}
								{#if o.requires_licence}<span class="badge status-draft">{t('app.provider.licence_needed')}</span>{/if}
							</div>
						</div>
						{#if listing.status !== 'archived'}
							<div class="row">
								<button class="btn" type="button" onclick={() => startEdit(o)}>{t('app.common.edit')}</button>
								<button class="btn btn-danger" type="button" onclick={() => remove(o)}>{t('app.common.remove')}</button>
							</div>
						{/if}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
</section>

{#if listing.status !== 'archived' && editing === null}
	<form class="card" onsubmit={add} novalidate>
		<h2>{t('app.provider.add_service')}</h2>
		{#if !picked}
			<div class="field">
				<label for="svc-q">{t('app.provider.find_service')}</label>
				<input id="svc-q" type="search" autocomplete="off" bind:value={q} oninput={search} placeholder={t('app.provider.find_service_hint')} />
			</div>
			<ul class="list-plain picks" aria-live="polite">
				{#each results as s (s.id)}
					<li><button class="btn-link" type="button" onclick={() => (picked = s)}>{s.name} <span class="muted">· {s.category_name}</span></button></li>
				{/each}
			</ul>
			{#if errors.service_id}<p class="field-error">{fieldText(errors.service_id)}</p>{/if}
		{:else}
			<p><strong>{picked.name}</strong> <span class="muted">· {picked.category_name}</span>
				<button class="btn-link" type="button" onclick={() => (picked = null)}>{t('app.provider.change')}</button></p>
			{#if picked.requires_licence}<p class="notice">{t('app.provider.licence_note')}</p>{/if}
			{@render pricing('new')}
			<button class="btn btn-primary" type="submit" disabled={busy}>{t('app.common.add')}</button>
		{/if}
	</form>
{/if}

<style>
	.offers li { border-bottom: 1px solid var(--color-border, #e2e8f0); padding: 0.75rem 0; }
	.offer-row { display: flex; justify-content: space-between; gap: 1rem; flex-wrap: wrap; align-items: center; }
	.picks li { padding: 0.2rem 0; }
</style>
