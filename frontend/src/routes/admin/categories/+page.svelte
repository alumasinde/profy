<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import LifecycleActions from '#lib/components/LifecycleActions.svelte';
	import MetaFields from '#lib/components/MetaFields.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Category {
		id: string;
		parent_id: string | null;
		slug: string;
		path: string;
		name: string;
		description: string;
		sort_order: number;
		status: string;
		meta_title: string | null;
		meta_description: string | null;
		service_count: number;
	}
	let list = $state<Category[]>([]);
	let error = $state<unknown>(null);
	let createForm = $state({ name: '', parent_id: '' });
	let createErrors = $state<Record<string, string>>({});
	let editing = $state<string | null>(null);
	let form = $state({ name: '', slug: '', description: '', sort_order: '0', parent_id: '', meta_title: '', meta_description: '' });
	let errors = $state<Record<string, string>>({});

	const top = $derived(list.filter((c) => !c.parent_id));
	const childrenOf = (id: string) => list.filter((c) => c.parent_id === id);

	async function load() {
		try {
			list = (await api.get<Category[]>('/admin/categories')).data;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		createErrors = {};
		error = null;
		try {
			await api.post('/admin/categories', { name: createForm.name, parent_id: createForm.parent_id || null });
			createForm = { name: '', parent_id: '' };
			await load();
		} catch (err) {
			error = err;
			if (err instanceof ApiError) createErrors = err.fields;
		}
	}

	function edit(c: Category) {
		editing = c.id;
		errors = {};
		form = { name: c.name, slug: c.slug, description: c.description, sort_order: String(c.sort_order), parent_id: c.parent_id ?? '',
			meta_title: c.meta_title ?? '', meta_description: c.meta_description ?? '' };
	}

	async function patch(id: string, body: Record<string, unknown>) {
		error = null;
		errors = {};
		try {
			await api.patch('/admin/categories/' + id, body);
			await load();
			return true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
			return false;
		}
	}
	async function saveEdit(e: SubmitEvent) {
		e.preventDefault();
		if (await patch(editing!, { ...form, sort_order: Number(form.sort_order) || 0 })) editing = null;
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.categories')}</h1>
<FormError {error} />
{#if can('categories.create')}
	<form class="card" onsubmit={create} novalidate>
		<h2>{t('app.admin.new_category')}</h2>
		<div class="grid-2">
			<Field id="c-name" label={t('app.common.name')} bind:value={createForm.name} error={createErrors.name ?? createErrors.slug} />
			<div class="field">
				<label for="c-parent">{t('app.admin.parent_category')}</label>
				<select id="c-parent" bind:value={createForm.parent_id}>
					<option value="">{t('app.admin.top_level')}</option>
					{#each top.filter((c) => c.status !== 'archived') as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
				</select>
			</div>
		</div>
		<button class="btn btn-primary" type="submit" disabled={!createForm.name.trim()}>{t('app.common.create')}</button>
	</form>
{/if}

{#snippet row(c: Category, nested: boolean)}
	<li class={nested ? 'indent' : ''}>
		<span><strong>{c.name}</strong> <code class="muted">{c.path}</code> <StatusBadge status={c.status} />
			<span class="badge">{t('app.admin.service_count', { count: c.service_count })}</span></span>
		{#if can('categories.update') || can('categories.publish') || can('categories.archive')}
			<button class="btn" type="button" onclick={() => (editing === c.id ? (editing = null) : edit(c))}>{t('app.common.edit')}</button>
		{/if}
		{#if editing === c.id}
			<div class="card full">
				{#if can('categories.update')}
					<form onsubmit={saveEdit} novalidate>
						<div class="grid-2">
							<Field id="e-name" label={t('app.common.name')} bind:value={form.name} error={errors.name} />
							<Field id="e-slug" label={t('app.common.slug')} bind:value={form.slug} error={errors.slug} />
							<div class="field">
								<label for="e-parent">{t('app.admin.parent_category')}</label>
								<select id="e-parent" bind:value={form.parent_id}>
									<option value="">{t('app.admin.top_level')}</option>
									{#each top.filter((x) => x.id !== c.id && x.status !== 'archived') as x (x.id)}<option value={x.id}>{x.name}</option>{/each}
								</select>
								{#if errors.parent_id}<p class="field-error">{t('fields.' + errors.parent_id)}</p>{/if}
							</div>
							<Field id="e-sort" label={t('app.common.sort_order')} inputmode="numeric" bind:value={form.sort_order} />
						</div>
						<Field id="e-desc" label={t('app.common.description')} optional rows={3} bind:value={form.description} error={errors.description} />
						<MetaFields bind:meta_title={form.meta_title} bind:meta_description={form.meta_description} {errors} />
						<button class="btn btn-primary" type="submit">{t('app.common.save')}</button>
					</form>
				{/if}
				<h3>{t('app.common.status')}</h3>
				<LifecycleActions status={c.status} prefix="categories" onchange={(s) => patch(c.id, { status: s })} />
			</div>
		{/if}
	</li>
{/snippet}

<section class="card">
	<ul class="list-plain">
		{#each top as c (c.id)}
			{@render row(c, false)}
			{#each childrenOf(c.id) as child (child.id)}{@render row(child, true)}{/each}
		{:else}
			<li class="muted">{t('app.admin.empty')}</li>
		{/each}
	</ul>
</section>
