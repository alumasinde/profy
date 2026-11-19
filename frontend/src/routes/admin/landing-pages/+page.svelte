<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, formatDate, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Row {
		service: { slug: string; name: string }; location: { slug: string; label: string; type: string }; path: string;
		provider_count: number; indexed: boolean; robots_mode: string; meta_title: string; meta_description: string; intro: string;
		below_since: string | null; evaluated_at: string | null;
	}
	let filterState = $state('');
	let service = $state('');
	let rows = $state<Row[]>([]);
	let next = $state<string | null>(null);
	let minProviders = $state(3);
	let error = $state<unknown>(null);
	let editing = $state<Row | null>(null);
	let form = $state({ meta_title: '', meta_description: '', intro: '', robots_mode: 'auto' });
	let errors = $state<Record<string, string>>({});
	let busy = $state(false);
	let evaluated = $state<number | null>(null);

	async function load(c: string | null = null) {
		const p = new URLSearchParams();
		if (filterState) p.set('state', filterState);
		if (service.trim()) p.set('service', service.trim());
		if (c) p.set('cursor', c);
		try {
			const res = await api.get<Row[]>('/admin/landing-pages?' + p);
			rows = c ? [...rows, ...res.data] : res.data;
			next = (res.meta?.next_cursor as string) || null;
			minProviders = (res.meta?.min_providers as number) ?? 3;
		} catch (e) {
			error = e;
		}
	}
	$effect(() => {
		load();
	});
	function edit(r: Row) {
		editing = r;
		form = { meta_title: r.meta_title, meta_description: r.meta_description, intro: r.intro, robots_mode: r.robots_mode };
		errors = {};
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!editing) return;
		busy = true;
		errors = {};
		error = null;
		try {
			await api.put('/admin/landing-pages', { service: editing.service.slug, location: editing.location.slug, ...form });
			editing = null;
			await load();
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}
	async function evaluate() {
		error = null;
		try {
			evaluated = (await api.post<{ rows: number }>('/admin/landing-pages/evaluate')).data.rows;
			await load();
		} catch (e) {
			error = e;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.landing_pages')}</h1>
<p class="lead">{t('app.admin.discovery.landing_lead', { n: minProviders })}</p>
<FormError {error} />
<div class="toolbar">
	<select aria-label={t('app.admin.discovery.state')} bind:value={filterState} onchange={() => load()}>
		<option value="">{t('app.common.filter_all')}</option>
		{#each ['indexed', 'thin', 'overridden'] as s (s)}<option value={s}>{t('app.admin.discovery.state_' + s)}</option>{/each}
	</select>
	<input type="search" aria-label={t('app.admin.discovery.service_slug')} placeholder={t('app.admin.discovery.service_slug')} bind:value={service} onchange={() => load()} />
	{#if can('seo.update_pages')}<button class="btn" type="button" onclick={evaluate}>{t('app.admin.discovery.evaluate')}</button>{/if}
</div>
{#if evaluated !== null}<p class="success" role="status">{t('app.admin.discovery.evaluated', { n: evaluated })}</p>{/if}

{#if editing}
	<form class="card" onsubmit={save} novalidate>
		<h2>{editing.service.name} — {editing.location.label}</h2>
		<p><a href={editing.path} target="_blank" rel="noopener" data-sveltekit-reload>{editing.path}</a></p>
		<div class="field">
			<label for="l-robots">{t('app.admin.discovery.robots')}</label>
			<select id="l-robots" bind:value={form.robots_mode}>
				{#each ['auto', 'index', 'noindex'] as m (m)}<option value={m}>{t('app.admin.discovery.robots_' + m)}</option>{/each}
			</select>
		</div>
		<div class="field">
			<label for="l-mt">{t('app.common.meta_title')} <span class="muted optional">({t('app.common.optional')})</span></label>
			<input id="l-mt" maxlength="160" bind:value={form.meta_title} />
			{#if errors.meta_title}<p class="field-error">{fieldText(errors.meta_title)}</p>{/if}
		</div>
		<div class="field">
			<label for="l-md">{t('app.common.meta_description')} <span class="muted optional">({t('app.common.optional')})</span></label>
			<textarea id="l-md" rows="2" maxlength="320" bind:value={form.meta_description}></textarea>
		</div>
		<div class="field">
			<label for="l-intro">{t('app.admin.discovery.intro')} <span class="muted optional">({t('app.common.optional')})</span></label>
			<textarea id="l-intro" rows="4" maxlength="2000" bind:value={form.intro}></textarea>
			<p class="hint">{t('app.admin.page_body_hint')}</p>
		</div>
		<div class="row">
			<button class="btn btn-primary" type="submit" disabled={busy}>{t('app.common.save')}</button>
			<button class="btn" type="button" onclick={() => (editing = null)}>{t('app.common.cancel')}</button>
		</div>
	</form>
{/if}

<div class="table-wrap">
	<table>
		<thead><tr><th>{t('app.admin.discovery.page')}</th><th>{t('app.admin.discovery.providers')}</th><th>{t('app.admin.discovery.indexing')}</th><th></th></tr></thead>
		<tbody>
			{#each rows as r (r.path)}
				<tr>
					<td><a href={r.path} target="_blank" rel="noopener" data-sveltekit-reload>{r.service.name} — {r.location.label}</a></td>
					<td>{r.provider_count}</td>
					<td>
						<span class={'badge ' + (r.indexed ? 'status-published' : 'status-draft')}>{r.indexed ? t('app.admin.discovery.state_indexed') : t('app.admin.discovery.state_thin')}</span>
						{#if r.robots_mode !== 'auto'}<span class="badge status-archived">{t('app.admin.discovery.robots_' + r.robots_mode)}</span>{/if}
						{#if r.below_since && r.indexed}<p class="hint">{t('app.admin.discovery.below_since', { date: formatDate(r.below_since) })}</p>{/if}
					</td>
					<td>{#if can('seo.update_pages')}<button class="btn" type="button" onclick={() => edit(r)}>{t('app.common.edit')}</button>{/if}</td>
				</tr>
			{:else}<tr><td colspan="4" class="muted">{t('app.admin.discovery.landing_empty')}</td></tr>{/each}
		</tbody>
	</table>
</div>
{#if next}<button class="btn" type="button" onclick={() => load(next)}>{t('app.common.loading_more')}</button>{/if}
