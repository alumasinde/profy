<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Item {
		label: string;
		url: string;
		is_active: boolean;
	}
	const menus = ['header', 'footer'] as const;
	let items = $state<Record<string, Item[]>>({ header: [], footer: [] });
	let errors = $state<Record<string, Record<string, string>>>({ header: {}, footer: {} });
	let error = $state<unknown>(null);
	let saved = $state('');
	const editable = $derived(can('navigation.update'));

	$effect(() => {
		for (const m of menus) {
			api.get<Item[]>('/admin/navigation/' + m).then((r) => (items[m] = r.data)).catch((e) => (error = e));
		}
	});

	function move(m: string, i: number, d: number) {
		const list = [...items[m]];
		const j = i + d;
		if (j < 0 || j >= list.length) return;
		[list[i], list[j]] = [list[j], list[i]];
		items[m] = list;
	}

	async function save(m: string) {
		error = null;
		saved = '';
		errors[m] = {};
		try {
			items[m] = (await api.put<Item[]>('/admin/navigation/' + m, { items: items[m] })).data;
			saved = m;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors[m] = err.fields;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.navigation')}</h1>
<p class="muted">{t('app.admin.menu_hint')}</p>
<FormError {error} />
{#each menus as m (m)}
	<section class="card">
		<h2>{t('app.admin.menu.' + m)}</h2>
		{#each items[m] as it, i (i)}
			<div class="menu-row">
				<div class="field">
					<label for={m + '-l-' + i}>{t('app.admin.link_label')}</label>
					<input id={m + '-l-' + i} bind:value={it.label} disabled={!editable} />
					{#if errors[m]['items.' + i + '.label']}<p class="field-error">{fieldText(errors[m]['items.' + i + '.label'])}</p>{/if}
				</div>
				<div class="field">
					<label for={m + '-u-' + i}>{t('app.admin.link_url')}</label>
					<input id={m + '-u-' + i} bind:value={it.url} disabled={!editable} />
					{#if errors[m]['items.' + i + '.url']}<p class="field-error">{fieldText(errors[m]['items.' + i + '.url'])}</p>{/if}
				</div>
				<label class="check"><input type="checkbox" bind:checked={it.is_active} disabled={!editable} /> {t('app.admin.item_active')}</label>
				{#if editable}
					<div class="row">
						<button class="btn" type="button" aria-label={t('app.common.move_up')} onclick={() => move(m, i, -1)}>↑</button>
						<button class="btn" type="button" aria-label={t('app.common.move_down')} onclick={() => move(m, i, 1)}>↓</button>
						<button class="btn btn-danger" type="button" onclick={() => (items[m] = items[m].filter((_, j) => j !== i))}>{t('app.common.remove')}</button>
					</div>
				{/if}
			</div>
		{/each}
		{#if editable}
			<p class="row">
				<button class="btn" type="button" onclick={() => (items[m] = [...items[m], { label: '', url: '/', is_active: true }])}>{t('app.admin.add_item')}</button>
				<button class="btn btn-primary" type="button" onclick={() => save(m)}>{t('app.admin.save_menu')}</button>
				{#if saved === m}<span class="success" role="status">{t('app.common.saved')}</span>{/if}
			</p>
		{/if}
	</section>
{/each}
