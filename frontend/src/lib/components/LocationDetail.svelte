<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api.ts';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';
	import Field from './Field.svelte';
	import FormError from './FormError.svelte';
	import LinesEditor from './LinesEditor.svelte';
	import MetaFields from './MetaFields.svelte';
	import StatusBadge from './StatusBadge.svelte';

	interface Loc {
		id: string;
		type: 'county' | 'town' | 'area';
		name: string;
		slug: string;
		path: string;
		code?: string;
		parent_id: string | null;
		parent_name?: string;
		is_selectable: boolean;
		is_indexable: boolean;
		status: string;
		merged_into_id?: string;
		meta_title: string | null;
		meta_description: string | null;
		child_count: number;
		centroid: { latitude: number; longitude: number } | null;
		aliases?: string[];
	}

	let { type, id }: { type: 'county' | 'town' | 'area'; id: string } = $props();
	const plural = { county: 'counties', town: 'towns', area: 'areas' } as const;
	const childType = { county: 'town', town: 'area', area: null } as const;
	const parentType = { county: null, town: 'county', area: 'town' } as const;

	let loc = $state<Loc | null>(null);
	let form = $state({ name: '', slug: '', is_selectable: false, is_indexable: false, meta_title: '', meta_description: '' });
	let point = $state({ latitude: '', longitude: '' });
	let errors = $state<Record<string, string>>({});
	let pointErrors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let saved = $state('');

	let children = $state<Loc[]>([]);
	let childNext = $state<string | null>(null);
	let childQ = $state('');
	let newChild = $state('');
	let childErrors = $state<Record<string, string>>({});

	let mergeQ = $state('');
	let mergeOptions = $state<Loc[]>([]);
	let mergeTarget = $state('');

	const base = $derived('/admin/locations/' + plural[type] + '/' + encodeURIComponent(id));
	const merged = $derived(!!loc?.merged_into_id);

	function fill(l: Loc) {
		loc = l;
		form = { name: l.name, slug: l.slug, is_selectable: l.is_selectable, is_indexable: l.is_indexable,
			meta_title: l.meta_title ?? '', meta_description: l.meta_description ?? '' };
		point = { latitude: l.centroid ? String(l.centroid.latitude) : '', longitude: l.centroid ? String(l.centroid.longitude) : '' };
	}

	async function loadChildren(cursor: string | null = null) {
		const ct = childType[type];
		if (!ct) return;
		const params = new URLSearchParams({ parent_id: id, limit: '100' });
		if (childQ.trim()) params.set('q', childQ.trim());
		if (cursor) params.set('cursor', cursor);
		const res = await api.get<Loc[]>('/admin/locations/' + plural[ct] + '?' + params);
		children = cursor ? [...children, ...res.data] : res.data;
		childNext = (res.meta?.next_cursor as string | null) ?? null;
	}

	$effect(() => {
		api.get<Loc>(base).then((r) => fill(r.data)).catch((e) => (error = e));
		loadChildren().catch((e) => (error = e));
	});

	async function run(fn: () => Promise<Loc>, tag: string, onErrors?: (f: Record<string, string>) => void) {
		error = null;
		saved = '';
		try {
			fill(await fn());
			saved = tag;
		} catch (err) {
			error = err;
			if (err instanceof ApiError && onErrors) onErrors(err.fields);
		}
	}

	function save(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		const body: Record<string, unknown> = { is_selectable: form.is_selectable, is_indexable: form.is_indexable,
			meta_title: form.meta_title, meta_description: form.meta_description };
		if (type !== 'county') Object.assign(body, { name: form.name, slug: form.slug });
		run(async () => (await api.patch<Loc>(base, body)).data, 'main', (f) => (errors = f));
	}
	function savePoint(e: SubmitEvent) {
		e.preventDefault();
		pointErrors = {};
		run(async () => (await api.put<Loc>(base + '/centroid', { latitude: Number(point.latitude), longitude: Number(point.longitude) })).data,
			'point', (f) => (pointErrors = f));
	}
	function setStatus(status: string) {
		if (status === 'archived' && !confirm(t('app.common.confirm'))) return;
		run(async () => (await api.patch<Loc>(base, { status })).data, 'status');
	}

	async function addChild(e: SubmitEvent) {
		e.preventDefault();
		const ct = childType[type];
		if (!ct) return;
		childErrors = {};
		try {
			const c = (await api.post<Loc>('/admin/locations/' + plural[ct], { name: newChild, parent_id: id })).data;
			newChild = '';
			goto('/admin/locations/' + plural[ct] + '/' + c.id);
		} catch (err) {
			error = err;
			if (err instanceof ApiError) childErrors = err.fields;
		}
	}

	async function searchMerge() {
		const params = new URLSearchParams({ q: mergeQ.trim(), status: 'active', limit: '20' });
		mergeOptions = (await api.get<Loc[]>('/admin/locations/' + plural[type] + '?' + params)).data.filter((x) => x.id !== id);
	}
	async function merge() {
		const target = mergeOptions.find((x) => x.id === mergeTarget);
		if (!target || !loc || !confirm(t('app.admin.merge_confirm', { source: loc.name, target: target.name }))) return;
		error = null;
		try {
			await api.post('/admin/locations/merge', { type, source_id: id, target_id: target.id });
			goto('/admin/locations/' + plural[type] + '/' + target.id);
		} catch (err) {
			error = err;
		}
	}
</script>

<p class="breadcrumbs">
	<a href="/admin">{t('app.admin.title')}</a> / <a href="/admin/locations">{t('app.admin.locations')}</a>
	{#if loc?.parent_id && parentType[type]}
		/ <a href={'/admin/locations/' + plural[parentType[type]!] + '/' + loc.parent_id}>{loc.parent_name}</a>
	{/if}
</p>
<FormError {error} />
{#if loc}
	<h1>{loc.name} <span class="badge">{t('app.admin.' + type)}</span> <StatusBadge status={loc.status} /></h1>
	<p class="muted"><code>{loc.path}</code>{#if loc.code}<span> · {loc.code}</span>{/if}</p>
	{#if merged}
		<p class="notice">{t('app.admin.merged_into')} — <a href={'/admin/locations/' + plural[type] + '/' + loc.merged_into_id}>{t('app.common.open')}</a></p>
	{/if}

	<form class="card" onsubmit={save} novalidate>
		<h2>{t('app.common.details')}</h2>
		{#if type === 'county'}
			<p class="muted">{t('app.admin.official_data')}</p>
		{:else}
			<div class="grid-2">
				<Field id="l-name" label={t('app.common.name')} bind:value={form.name} error={errors.name} />
				<Field id="l-slug" label={t('app.common.slug')} bind:value={form.slug} error={errors.slug} />
			</div>
		{/if}
		<label class="check"><input type="checkbox" bind:checked={form.is_selectable} /> {t('app.admin.selectable')}</label>
		<label class="check"><input type="checkbox" bind:checked={form.is_indexable} /> {t('app.admin.indexable')}</label>
		<MetaFields bind:meta_title={form.meta_title} bind:meta_description={form.meta_description} {errors} />
		{#if saved === 'main'}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
		{#if can('locations.update') && !merged}<button class="btn btn-primary" type="submit">{t('app.common.save')}</button>{/if}
	</form>

	{#if childType[type]}
		{@const ct = childType[type]!}
		<section class="card">
			<h2>{t('app.admin.' + plural[ct])} <span class="badge">{t('app.admin.children', { count: loc.child_count })}</span></h2>
			<form class="toolbar" onsubmit={(e) => { e.preventDefault(); loadChildren(); }}>
				<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={childQ} />
			</form>
			<ul class="list-plain">
				{#each children as c (c.id)}
					<li><a href={'/admin/locations/' + plural[ct] + '/' + c.id}>{c.name}</a>
						<span>{#if c.is_selectable}<span class="badge">{t('app.admin.selectable')}</span>{/if}<StatusBadge status={c.status} /></span></li>
				{:else}
					<li class="muted">{t('app.admin.empty')}</li>
				{/each}
			</ul>
			{#if childNext}<button class="btn" type="button" onclick={() => loadChildren(childNext)}>{t('app.common.loading_more')}</button>{/if}
			{#if can('locations.create') && loc.status === 'active'}
				<form class="row" onsubmit={addChild} novalidate>
					<input aria-label={t('app.admin.new_' + ct)} placeholder={t('app.admin.new_' + ct)} bind:value={newChild} />
					<button class="btn btn-primary" type="submit" disabled={!newChild.trim()}>{t('app.common.add')}</button>
				</form>
				{#if childErrors.name || childErrors.slug}<p class="field-error">{fieldText(childErrors.name ?? childErrors.slug)}</p>{/if}
			{/if}
		</section>
	{/if}

	<section class="card">
		<LinesEditor id="aliases" label={t('app.admin.aliases')} hint={t('app.admin.aliases_hint')} items={loc.aliases ?? []}
			saveLabel={t('app.admin.save_aliases')} disabled={!can('locations.manage_aliases') || merged}
			onsave={async (list) => fill((await api.put<Loc>(base + '/aliases', { aliases: list })).data)} />
	</section>

	<form class="card" onsubmit={savePoint} novalidate>
		<h2>{t('app.admin.centroid')}</h2>
		<p class="hint">{t('app.admin.centroid_hint')}</p>
		<div class="grid-2">
			<Field id="lat" label={t('app.admin.latitude')} inputmode="text" bind:value={point.latitude} error={pointErrors.latitude} />
			<Field id="lng" label={t('app.admin.longitude')} inputmode="text" bind:value={point.longitude} error={pointErrors.longitude} />
		</div>
		{#if saved === 'point'}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
		{#if can('locations.manage_coordinates') && !merged}
			<div class="row">
				<button class="btn btn-primary" type="submit" disabled={!point.latitude || !point.longitude}>{t('app.admin.save_centroid')}</button>
				{#if loc.centroid}<button class="btn" type="button" onclick={() => run(async () => (await api.del<Loc>(base + '/centroid')).data, 'point')}>{t('app.admin.clear_centroid')}</button>{/if}
			</div>
		{/if}
	</form>

	{#if type !== 'county' && !merged}
		{#if can('locations.archive')}
			<section class="card">
				<h2>{t('app.common.status')}</h2>
				{#if loc.status === 'active'}
					<button class="btn btn-danger" type="button" onclick={() => setStatus('archived')}>{t('app.lifecycle.archive')}</button>
				{:else}
					<button class="btn" type="button" onclick={() => setStatus('active')}>{t('app.lifecycle.reactivate')}</button>
				{/if}
			</section>
		{/if}
		{#if can('locations.merge') && loc.status === 'active'}
			<section class="card danger-zone">
				<h2>{t('app.admin.merge_title')}</h2>
				<p class="hint">{t('app.admin.merge_hint', { type: t('app.admin.' + type).toLowerCase() })}</p>
				<form class="row" onsubmit={(e) => { e.preventDefault(); searchMerge(); }}>
					<input type="search" aria-label={t('app.common.search')} placeholder={t('app.common.search')} bind:value={mergeQ} />
					<button class="btn" type="submit">{t('app.common.search')}</button>
				</form>
				{#if mergeOptions.length}
					<div class="field">
						<label for="merge-target">{t('app.admin.merge_target')}</label>
						<select id="merge-target" bind:value={mergeTarget}>
							<option value="" disabled>—</option>
							{#each mergeOptions as o (o.id)}<option value={o.id}>{o.name} ({o.parent_name})</option>{/each}
						</select>
					</div>
					<button class="btn btn-danger" type="button" disabled={!mergeTarget} onclick={merge}>{t('app.admin.merge_button')}</button>
				{/if}
			</section>
		{/if}
	{/if}
{/if}
