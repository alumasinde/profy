<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import Field from '#lib/components/Field.svelte';
	import FormError from '#lib/components/FormError.svelte';
	import { i18n, loadLocale, t } from '#lib/i18n.svelte.ts';
	import { refreshSession, session } from '#lib/session.svelte.ts';

	let form = $state({ first_name: '', middle_name: '', last_name: '', preferred_locale: 'en' });
	let busy = $state(false);
	let saved = $state(false);
	let error = $state<unknown>(null);
	let fieldErrors = $state<Record<string, string>>({});

	$effect(() => {
		const u = session.user;
		if (u) form = { first_name: u.first_name, middle_name: u.middle_name ?? '', last_name: u.last_name, preferred_locale: u.preferred_locale };
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		saved = false;
		error = null;
		fieldErrors = {};
		try {
			await api.patch('/customer/profile', { ...form, middle_name: form.middle_name.trim() === '' ? null : form.middle_name });
			await refreshSession();
			if (form.preferred_locale !== i18n.locale) await loadLocale(form.preferred_locale);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) fieldErrors = err.fields;
		} finally {
			busy = false;
		}
	}
</script>

{#if session.user}
	<h1>{t('app.account.greeting', { name: session.user.first_name })}</h1>

	<section class="card">
		<h2>{t('app.account.login_channels')}</h2>
		{#if session.user.phone}<p>{session.user.phone} · <span class="badge">{session.user.phone_verified ? t('app.account.verified') : t('app.account.not_verified')}</span></p>{/if}
		{#if session.user.email}<p>{session.user.email} · <span class="badge">{session.user.email_verified ? t('app.account.verified') : t('app.account.not_verified')}</span></p>{/if}
	</section>

	<section class="card">
		<h2>{t('app.account.profile_title')}</h2>
		<form onsubmit={save} novalidate>
			<Field id="first_name" label={t('app.fields.first_name')} autocomplete="given-name" bind:value={form.first_name} error={fieldErrors.first_name} />
			<Field id="middle_name" label={t('app.fields.middle_name')} autocomplete="additional-name" optional bind:value={form.middle_name} error={fieldErrors.middle_name} />
			<Field id="last_name" label={t('app.fields.last_name')} autocomplete="family-name" bind:value={form.last_name} error={fieldErrors.last_name} />
			<div class="field">
				<label for="locale">{t('app.fields.locale')}</label>
				<select id="locale" bind:value={form.preferred_locale}>
					{#each i18n.locales as l (l)}<option value={l}>{l}</option>{/each}
				</select>
			</div>
			<FormError {error} />
			{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
			<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? t('app.common.saving') : t('app.common.save')}</button>
		</form>
	</section>
{/if}
