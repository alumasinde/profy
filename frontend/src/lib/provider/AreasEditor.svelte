<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import FormError from '#lib/components/FormError.svelte';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import type { EditorTarget, Listing, LocationRef } from './types.ts';

	let { target, listing, onsaved }: { target: EditorTarget; listing: Listing; onsaved: (l: Listing) => void } = $props();

	let counties = $state<LocationRef[]>([]);
	let towns = $state<LocationRef[]>([]);
	let areas = $state<LocationRef[]>([]);
	let county = $state('');
	let town = $state('');
	let base = $state<LocationRef | null>(null);
	let radius = $state('');
	let coverage = $state<LocationRef[]>([]);
	let lat = $state('');
	let lng = $state('');
	let q = $state('');
	let found = $state<LocationRef[]>([]);
	let errors = $state<Record<string, string>>({});
	let error = $state<unknown>(null);
	let saved = $state(false);
	let busy = $state(false);
	let locating = $state(false);
	let geoError = $state('');

	$effect(() => {
		base = listing.base_area;
		radius = listing.service_radius_km === null ? '' : String(listing.service_radius_km);
		coverage = [...listing.coverage];
		lat = listing.pin ? String(listing.pin.latitude) : '';
		lng = listing.pin ? String(listing.pin.longitude) : '';
	});
	$effect(() => {
		api.get<LocationRef[]>('/locations/counties').then((r) => (counties = r.data)).catch((e) => (error = e));
	});

	async function pickCounty() {
		town = '';
		towns = [];
		areas = [];
		if (county) towns = (await api.get<LocationRef[]>('/locations/towns?county=' + county)).data;
	}
	async function pickTown() {
		areas = [];
		if (town) areas = (await api.get<LocationRef[]>('/locations/areas?town=' + town)).data;
	}

	let timer: ReturnType<typeof setTimeout> | undefined;
	function search() {
		clearTimeout(timer);
		timer = setTimeout(async () => {
			if (q.trim().length < 2) return (found = []);
			try {
				found = (await api.get<LocationRef[]>('/locations/search?q=' + encodeURIComponent(q.trim()))).data;
			} catch (e) {
				error = e;
			}
		}, 250);
	}
	function addCoverage(l: LocationRef) {
		if (!coverage.some((c) => c.type === l.type && c.id === l.id)) coverage = [...coverage, l];
		q = '';
		found = [];
	}

	function useMyLocation() {
		geoError = '';
		if (!('geolocation' in navigator)) return (geoError = t('app.provider.geo_unavailable'));
		locating = true;
		navigator.geolocation.getCurrentPosition(
			(p) => {
				lat = p.coords.latitude.toFixed(6);
				lng = p.coords.longitude.toFixed(6);
				locating = false;
			},
			() => {
				geoError = t('app.provider.geo_denied');
				locating = false;
			},
			{ enableHighAccuracy: true, timeout: 15000 }
		);
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		saved = false;
		error = null;
		errors = {};
		const pin = lat.trim() && lng.trim() ? { latitude: Number(lat), longitude: Number(lng) } : null;
		try {
			onsaved((await api.put<Listing>(target.base + '/service-areas', {
				base_area_id: base?.id ?? null,
				service_radius_km: radius.trim() === '' ? null : Number(radius),
				coverage: coverage.map((c) => ({ type: c.type, id: c.id })),
				pin
			})).data);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) errors = err.fields;
		} finally {
			busy = false;
		}
	}
	const errFor = (prefix: string) => Object.entries(errors).find(([k]) => k === prefix || k.startsWith(prefix + '.'))?.[1];
</script>

<form onsubmit={save} novalidate>
	<section class="card">
		<h2>{t('app.provider.base_area')}</h2>
		<p class="hint">{t('app.provider.base_area_hint')}</p>
		{#if base}<p><strong>{base.label}</strong> <button class="btn-link" type="button" onclick={() => (base = null)}>{t('app.provider.change')}</button></p>{/if}
		{#if !base}
			<div class="grid-3">
				<div class="field">
					<label for="a-county">{t('app.admin.county')}</label>
					<select id="a-county" bind:value={county} onchange={pickCounty}>
						<option value="">—</option>
						{#each counties as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label for="a-town">{t('app.admin.town')}</label>
					<select id="a-town" bind:value={town} onchange={pickTown} disabled={!county}>
						<option value="">—</option>
						{#each towns as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label for="a-area">{t('app.admin.area')}</label>
					<select id="a-area" disabled={!town} onchange={(ev) => (base = areas.find((a) => a.id === ev.currentTarget.value) ?? null)}>
						<option value="">—</option>
						{#each areas as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
					</select>
				</div>
			</div>
			{#if county && town && areas.length === 0}<p class="hint">{t('app.provider.no_areas')}</p>{/if}
		{/if}
		{#if errFor('base_area_id')}<p class="field-error">{fieldText(errFor('base_area_id') ?? '')}</p>{/if}
		<div class="field narrow">
			<label for="a-radius">{t('app.provider.radius')} <span class="muted optional">({t('app.common.optional')})</span></label>
			<input id="a-radius" inputmode="numeric" maxlength="2" bind:value={radius} />
			<p class="hint">{t('app.provider.radius_hint', { max: 50 })}</p>
			{#if errors.service_radius_km}<p class="field-error">{fieldText(errors.service_radius_km)}</p>{/if}
		</div>
	</section>

	<section class="card">
		<h2>{t('app.provider.coverage')}</h2>
		<p class="hint">{t('app.provider.coverage_hint')}</p>
		<ul class="list-plain chips">
			{#each coverage as c (c.type + c.id)}
				<li>{c.label} <span class="muted">({t('app.admin.' + c.type)})</span>
					<button class="btn-link" type="button" aria-label={t('app.common.remove') + ' ' + c.label} onclick={() => (coverage = coverage.filter((x) => x !== c))}>✕</button></li>
			{:else}<li class="muted">{t('app.provider.no_coverage')}</li>{/each}
		</ul>
		{#if errFor('coverage')}<p class="field-error">{fieldText(errFor('coverage') ?? '')}</p>{/if}
		<div class="field">
			<label for="a-q">{t('app.provider.add_coverage')}</label>
			<input id="a-q" type="search" autocomplete="off" bind:value={q} oninput={search} placeholder={t('app.provider.add_coverage_hint')} />
		</div>
		<ul class="list-plain" aria-live="polite">
			{#each found as l (l.type + l.id)}
				<li><button class="btn-link" type="button" onclick={() => addCoverage(l)}>{l.label} <span class="muted">({t('app.admin.' + l.type)})</span></button></li>
			{/each}
		</ul>
		{#if base && !coverage.some((c) => c.id === base?.id)}
			<button class="btn" type="button" onclick={() => base && addCoverage(base)}>{t('app.provider.add_base_to_coverage')}</button>
		{/if}
	</section>

	<section class="card">
		<h2>{t('app.provider.pin')}</h2>
		<p class="hint">{t('app.provider.pin_hint')}</p>
		<div class="grid-2">
			<div class="field"><label for="a-lat">{t('app.admin.latitude')}</label><input id="a-lat" inputmode="text" bind:value={lat} /></div>
			<div class="field"><label for="a-lng">{t('app.admin.longitude')}</label><input id="a-lng" inputmode="text" bind:value={lng} /></div>
		</div>
		{#if errFor('pin')}<p class="field-error">{fieldText(errFor('pin') ?? '')}</p>{/if}
		<div class="row">
			{#if !target.staff}<button class="btn" type="button" onclick={useMyLocation} disabled={locating}>{locating ? t('app.provider.locating') : t('app.provider.use_my_location')}</button>{/if}
			{#if lat || lng}<button class="btn-link" type="button" onclick={() => { lat = ''; lng = ''; }}>{t('app.provider.clear_pin')}</button>{/if}
		</div>
		{#if geoError}<p class="field-error" role="alert">{geoError}</p>{/if}
	</section>

	<FormError {error} />
	{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
	<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? t('app.common.saving') : t('app.common.save')}</button>
</form>

<style>
	.grid-3 { display: grid; gap: 0.75rem; grid-template-columns: 1fr; }
	@media (min-width: 720px) { .grid-3 { grid-template-columns: repeat(3, 1fr); } }
	.chips { display: flex; flex-wrap: wrap; gap: 0.4rem; }
	.chips li { border: 1px solid var(--color-border, #e2e8f0); border-radius: 999px; padding: 0.15rem 0.6rem; }
</style>
