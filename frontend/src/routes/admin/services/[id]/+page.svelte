<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import LifecycleActions from '#lib/components/LifecycleActions.svelte';
	import LinesEditor from '#lib/components/LinesEditor.svelte';
	import MetaFields from '#lib/components/MetaFields.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Item {
		id: string;
		category_id: string;
		category_name: string;
		slug: string;
		path: string;
		name: string;
		description: string;
		requires_licence: boolean;
		sort_order: number;
		status: string;
		meta_title: string | null;
		meta_description: string | null;
		synonyms: string[] | null;
	}
	interface Category {
		id: string;
		parent_id: string | null;
		name: string;
		status: string;
	}
	let it = $state<Item | null>(null);
	let cats = $state<Category[]>([]);
	let form = $state({ name: '', slug: '', description: '', category_id: '', requires_licence: false, sort_order: '0', meta_title: '', meta_description: '' });
	let error = $state<unknown>(null);
	let errors = $state<Record<string, string>>({});
	let saved = $state(false);
	const id = $derived(page.params.id ?? '');
	const base = $derived('/admin/services/' + encodeURIComponent(id));

	function fill(x: Item) {
		it = x;
		form = { name: x.name, slug: x.slug, description: x.description, category_id: x.category_id, requires_licence: x.requires_licence,
			sort_order: String(x.sort_order), meta_title: x.meta_title ?? '', meta_description: x.meta_description ?? '' };
	}
	$effect(() => {
		api.get<Item>(base).then((r) => fill(r.data)).catch((e) => (error = e));
		api.get<Category[]>('/admin/categories').then((r) => (cats = r.data)).catch(() => {});
	});

	async function patch(body: Record<string, unknown>) {
		error = null;
		errors = {};
		saved = false;
		try {
			fill((await api.patch<Item>(base, body)).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		}
	}
	const catLabel = (c: Category) => {
		const p = c.parent_id ? cats.find((x) => x.id === c.parent_id) : null;
		return p ? p.name + ' › ' + c.name : c.name;
	};
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a> / <a href="/admin/services">{t('app.admin.services')}</a></p>
<FormError {error} />
{#if it}
	<h1>{it.name} <StatusBadge status={it.status} /></h1>
	<form class="card" onsubmit={(e) => { e.preventDefault(); patch({ ...form, sort_order: Number(form.sort_order) || 0 }); }} novalidate>
		<div class="grid-2">
			<Field id="name" label={t('app.common.name')} bind:value={form.name} error={errors.name} />
			<Field id="slug" label={t('app.common.slug')} hint={t('app.admin.page_path') + ': /services/' + form.slug} bind:value={form.slug} error={errors.slug} />
			<div class="field">
				<label for="cat">{t('app.admin.category')}</label>
				<select id="cat" bind:value={form.category_id}>
					{#each cats.filter((c) => c.status !== 'archived') as c (c.id)}<option value={c.id}>{catLabel(c)}</option>{/each}
				</select>
			</div>
			<Field id="sort" label={t('app.common.sort_order')} inputmode="numeric" bind:value={form.sort_order} />
		</div>
		<Field id="desc" label={t('app.common.description')} optional rows={3} bind:value={form.description} error={errors.description} />
		<label class="check"><input type="checkbox" bind:checked={form.requires_licence} /> {t('app.admin.requires_licence')}</label>
		<MetaFields bind:meta_title={form.meta_title} bind:meta_description={form.meta_description} {errors} />
		{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
		{#if can('services.update')}<button class="btn btn-primary" type="submit">{t('app.common.save')}</button>{/if}
	</form>
	<section class="card">
		<LinesEditor id="synonyms" label={t('app.admin.synonyms')} hint={t('app.admin.synonyms_hint')} items={it.synonyms ?? []}
			saveLabel={t('app.admin.save_synonyms')} disabled={!can('services.update')}
			onsave={async (list) => fill((await api.put<Item>(base + '/synonyms', { synonyms: list })).data)} />
	</section>
	<section class="card">
		<h2>{t('app.common.status')}</h2>
		<LifecycleActions status={it.status} prefix="services" onchange={(s) => patch({ status: s })} />
	</section>
{/if}
