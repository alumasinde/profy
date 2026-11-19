<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, i18n, t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	interface Setting {
		key: string;
		kind: string;
		group: string;
		required: boolean;
		max_length?: number;
		value: unknown;
	}
	interface Link {
		label: string;
		url: string;
	}

	let defs = $state<Setting[]>([]);
	let values = $state<Record<string, unknown>>({});
	let initial = '';
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let saved = $state(false);
	let busy = $state(false);

	const groups = $derived([...new Set(defs.map((d) => d.group))]);
	const editable = $derived(can('platform.settings.update'));

	function load(list: Setting[]) {
		defs = list;
		const v: Record<string, unknown> = {};
		for (const d of list) v[d.key] = structuredClone(d.value);
		values = v;
		initial = JSON.stringify(v);
	}
	$effect(() => {
		api.get<Setting[]>('/admin/settings').then((r) => load(r.data)).catch((e) => (error = e));
	});

	// Same WCAG formula as the server; the server has the final say.
	function lum(hex: string): number {
		const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
		return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
	}
	function contrast(a: string, b: string): number {
		if (!/^#[0-9a-f]{6}$/i.test(a)) return 0;
		const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
		return (x + 0.05) / (y + 0.05);
	}
	function colorRatio(key: string): number {
		const v = String(values[key] ?? '');
		return key === 'accent_color' ? contrast(v, '#f8fafc') : Math.min(contrast(v, '#ffffff'), contrast(v, '#f8fafc'));
	}
	const minRatio = (key: string) => (key === 'accent_color' ? 3 : 4.5);

	function links(): Link[] {
		return (values.social_links as Link[]) ?? [];
	}
	function channels(): string[] {
		return (values.otp_channels as string[]) ?? [];
	}
	function toggleChannel(c: string, on: boolean) {
		const set = new Set(channels());
		if (on) set.add(c);
		else set.delete(c);
		values.otp_channels = [...set];
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		saved = false;
		error = null;
		errors = {};
		const before = JSON.parse(initial);
		const patch: Record<string, unknown> = {};
		for (const d of defs) {
			if (JSON.stringify(values[d.key]) !== JSON.stringify(before[d.key])) patch[d.key] = values[d.key];
		}
		if (Object.keys(patch).length === 0) {
			busy = false;
			saved = true;
			return;
		}
		try {
			load((await api.patch<Setting[]>('/admin/settings', patch)).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}
</script>

<p class="breadcrumbs"><a href="/admin">{t('app.admin.title')}</a></p>
<h1>{t('app.admin.settings')}</h1>
<form onsubmit={save} novalidate>
	{#each groups as g (g)}
		{#if g !== 'security' || can('notification_channels.configure')}
			<section class="card">
				<h2>{t('app.admin.settings_groups.' + g)}</h2>
				<div class="grid-2">
					{#each defs.filter((d) => d.group === g) as d (d.key)}
						<div class="field">
							<label for={'s-' + d.key}>{t('app.admin.setting.' + d.key)}{#if !d.required && d.kind !== 'social'}<span class="muted optional">({t('app.common.optional')})</span>{/if}</label>
							{#if d.kind === 'color'}
								<div class="row">
									<input id={'s-' + d.key + '-picker'} type="color" aria-label={t('app.admin.setting.' + d.key)} bind:value={values[d.key]} disabled={!editable} />
									<input id={'s-' + d.key} type="text" maxlength="7" bind:value={values[d.key]} disabled={!editable} />
								</div>
								{@const ratio = colorRatio(d.key)}
								<p class={ratio >= minRatio(d.key) ? 'hint success' : 'field-error'}>
									{t('app.admin.contrast', { ratio: ratio.toFixed(1) })} — {ratio >= minRatio(d.key) ? t('app.admin.contrast_ok') : t('app.admin.contrast_bad')}
								</p>
							{:else if d.kind === 'social'}
								{#each links() as l, i (i)}
									<div class="row">
										<input aria-label={t('app.admin.link_label')} placeholder={t('app.admin.link_label')} bind:value={l.label} disabled={!editable} />
										<input aria-label={t('app.admin.link_url')} placeholder="https://" bind:value={l.url} disabled={!editable} />
										{#if editable}<button class="btn" type="button" onclick={() => (values.social_links = links().filter((_, j) => j !== i))}>{t('app.common.remove')}</button>{/if}
									</div>
								{/each}
								{#if editable}<button class="btn-link" type="button" onclick={() => (values.social_links = [...links(), { label: '', url: '' }])}>{t('app.admin.add_link')}</button>{/if}
							{:else if d.kind === 'channels'}
								{#each ['sms', 'email'] as c (c)}
									<label class="check"><input type="checkbox" checked={channels().includes(c)} onchange={(ev) => toggleChannel(c, ev.currentTarget.checked)} /> {t('app.admin.channel.' + c)}</label>
								{/each}
							{:else if d.kind === 'locale'}
								<select id={'s-' + d.key} bind:value={values[d.key]} disabled={!editable}>
									{#each i18n.locales as l (l)}<option value={l}>{l}</option>{/each}
								</select>
							{:else if d.key === 'site_description'}
								<textarea id={'s-' + d.key} rows="3" maxlength={d.max_length} bind:value={values[d.key]} disabled={!editable}></textarea>
							{:else}
								<input id={'s-' + d.key} type="text" maxlength={d.max_length} bind:value={values[d.key]} disabled={!editable}
									aria-invalid={errors[d.key] ? 'true' : undefined} />
								{#if d.kind === 'asset'}<p class="hint">{t('app.admin.asset_hint')}</p>{/if}
							{/if}
							{#if errors[d.key]}<p class="field-error">{fieldText(errors[d.key])}</p>{/if}
						</div>
					{/each}
				</div>
				{#if g === 'theme'}
					<p><span class="btn theme-preview" style:background={String(values.primary_color)} style:color="#fff">{t('app.admin.theme_preview')}</span>
						<span class="swatch" style:background={String(values.secondary_color)}></span>
						<span class="swatch" style:background={String(values.accent_color)}></span></p>
				{/if}
			</section>
		{/if}
	{/each}
	<FormError {error} />
	{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
	{#if editable}<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? t('app.common.saving') : t('app.common.save')}</button>{/if}
</form>
