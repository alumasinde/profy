<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import LifecycleActions from '#lib/components/LifecycleActions.svelte';
	import MetaFields from '#lib/components/MetaFields.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Page {
		id: string;
		slug: string;
		path: string;
		title: string;
		body: string;
		status: string;
		meta_title: string | null;
		meta_description: string | null;
		robots_index: boolean;
	}
	let p = $state<Page | null>(null);
	let form = $state({ title: '', slug: '', body: '', meta_title: '', meta_description: '', robots_index: true });
	let error = $state<unknown>(null);
	let errors = $state<Record<string, string>>({});
	let saved = $state(false);
	const id = $derived(page.params.id ?? '');

	function fill(x: Page) {
		p = x;
		form = { title: x.title, slug: x.slug, body: x.body, meta_title: x.meta_title ?? '', meta_description: x.meta_description ?? '', robots_index: x.robots_index };
	}
	$effect(() => {
		api.get<Page>('/admin/pages/' + encodeURIComponent(id)).then((r) => fill(r.data)).catch((e) => (error = e));
	});

	async function patch(body: Record<string, unknown>) {
		error = null;
		errors = {};
		saved = false;
		try {
			fill((await api.patch<Page>('/admin/pages/' + encodeURIComponent(id), body)).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a> / <a href="/admin/pages">{t('app.admin.pages')}</a></p>
<FormError {error} />
{#if p}
	<h1>{p.title} <StatusBadge status={p.status} /></h1>
	<p class="row">
		<a class="btn" href={'/preview/pages/' + p.id} target="_blank" rel="noopener" data-sveltekit-reload>{t('app.common.preview')}</a>
		{#if p.status === 'published'}<a class="btn" href={p.path} target="_blank" rel="noopener" data-sveltekit-reload>{t('app.common.view_public')}</a>{/if}
	</p>
	<form class="card" onsubmit={(e) => { e.preventDefault(); patch({ ...form, meta_title: form.meta_title, meta_description: form.meta_description }); }} novalidate>
		<Field id="title" label={t('app.admin.page_title')} bind:value={form.title} error={errors.title} />
		<Field id="slug" label={t('app.common.slug')} hint={t('app.admin.page_path') + ': /' + form.slug} bind:value={form.slug} error={errors.slug} />
		<Field id="body" label={t('app.admin.page_body')} hint={t('app.admin.page_body_hint')} rows={16} bind:value={form.body} error={errors.body} />
		<label class="check"><input type="checkbox" bind:checked={form.robots_index} /> {t('app.admin.robots_index')}</label>
		<MetaFields bind:meta_title={form.meta_title} bind:meta_description={form.meta_description} {errors} />
		{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
		{#if can('pages.update')}<button class="btn btn-primary" type="submit">{t('app.common.save')}</button>{/if}
	</form>
	<section class="card">
		<h2>{t('app.common.status')}</h2>
		<LifecycleActions status={p.status} prefix="pages" onchange={(s) => patch({ status: s })} />
	</section>
{/if}
