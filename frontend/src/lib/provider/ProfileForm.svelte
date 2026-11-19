<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import { profilePath, type EditorTarget, type Listing } from './types.ts';

	let { target, listing, onsaved }: { target: EditorTarget; listing: Listing; onsaved: (l: Listing) => void } = $props();

	let form = $state({ provider_type: '', display_name: '', description: '', years: '', slug: '', meta_title: '', meta_description: '', accept: false });
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let saved = $state(false);
	let busy = $state(false);

	$effect(() => {
		form = {
			provider_type: listing.provider_type, display_name: listing.display_name, description: listing.description,
			years: listing.years_experience === null ? '' : String(listing.years_experience), slug: listing.slug,
			meta_title: listing.meta_title ?? '', meta_description: listing.meta_description ?? '', accept: false
		};
	});
	const needsTerms = $derived(!target.staff && (!listing.terms_accepted || listing.terms_version !== listing.current_terms_version));
	const descLen = $derived([...form.description.trim()].length);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		saved = false;
		error = null;
		errors = {};
		const body: Record<string, unknown> = { display_name: form.display_name, description: form.description };
		if (listing.status === 'draft') body.provider_type = form.provider_type;
		if (form.years.trim() === '') body.clear_years_experience = true;
		else body.years_experience = Number(form.years);
		if (form.accept) body.accept_terms = true;
		if (target.staff) Object.assign(body, { slug: form.slug, meta_title: form.meta_title, meta_description: form.meta_description });
		try {
			onsaved((await api.patch<Listing>(profilePath(target), body)).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}
</script>

<form class="card" onsubmit={save} novalidate>
	<fieldset class="field" disabled={listing.status !== 'draft'}>
		<legend>{t('app.provider.type')}</legend>
		<label class="check"><input type="radio" name="ptype" value="individual" bind:group={form.provider_type} /> {t('app.provider.type_individual')}</label>
		<label class="check"><input type="radio" name="ptype" value="business" bind:group={form.provider_type} /> {t('app.provider.type_business')}</label>
		{#if listing.status !== 'draft'}<p class="hint">{t('app.provider.type_locked')}</p>{/if}
	</fieldset>
	<Field id="p-name" label={t('app.provider.display_name')} bind:value={form.display_name} maxlength={120} error={errors.display_name}
		hint={t('app.provider.display_name_hint')} />
	<Field id="p-desc" label={t('app.provider.description')} bind:value={form.description} rows={6} maxlength={3000} optional
		error={errors.description} hint={t('app.provider.description_hint', { n: descLen })} />
	<Field id="p-years" label={t('app.provider.years')} bind:value={form.years} inputmode="numeric" maxlength={2} optional error={errors.years_experience} />
	{#if target.staff}
		<div class="grid-2">
			<Field id="p-slug" label={t('app.common.slug')} bind:value={form.slug} error={errors.slug} hint={t('app.provider.slug_hint')} />
			<Field id="p-mt" label={t('app.common.meta_title')} bind:value={form.meta_title} maxlength={160} optional error={errors.meta_title} />
		</div>
		<Field id="p-md" label={t('app.common.meta_description')} bind:value={form.meta_description} rows={2} maxlength={320} optional error={errors.meta_description} />
	{/if}
	{#if needsTerms}
		<label class="check"><input type="checkbox" bind:checked={form.accept} /> {t('app.provider.accept_terms', { version: listing.current_terms_version })}</label>
		{#if errors.accept_terms}<p class="field-error">{fieldText(errors.accept_terms)}</p>{/if}
	{/if}
	<FormError {error} />
	{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
	<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? t('app.common.saving') : t('app.common.save')}</button>
</form>
