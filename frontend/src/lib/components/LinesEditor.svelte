<script lang="ts">
	// Edits a list of short strings as one-per-line text (synonyms, aliases).
	import { ApiError } from '#lib/api.ts';
	import { fieldText, t } from '#lib/i18n.svelte.ts';
	import FormError from './FormError.svelte';

	let {
		id,
		label,
		hint,
		items,
		saveLabel,
		onsave,
		disabled = false
	}: {
		id: string;
		label: string;
		hint: string;
		items: string[] | null | undefined;
		saveLabel: string;
		onsave: (list: string[]) => Promise<void>;
		disabled?: boolean;
	} = $props();

	let value = $state('');
	let error = $state<unknown>(null);
	let lineErrors = $state<string[]>([]);
	let saved = $state(false);
	$effect(() => {
		value = (items ?? []).join('\n');
	});

	async function save() {
		error = null;
		saved = false;
		lineErrors = [];
		const list = value.split('\n').map((s) => s.trim()).filter(Boolean);
		try {
			await onsave(list);
			saved = true;
		} catch (err) {
			error = err;
			if (err instanceof ApiError) {
				lineErrors = Object.entries(err.fields).map(([k, code]) => {
					const i = Number(k.split('.')[1]);
					return (Number.isFinite(i) ? '"' + list[i] + '": ' : '') + fieldText(code);
				});
			}
		}
	}
</script>

<div class="field">
	<label for={id}>{label}</label>
	<textarea {id} rows="5" bind:value aria-describedby={id + '-hint'} {disabled}></textarea>
	<p class="hint" id={id + '-hint'}>{hint}</p>
	{#each lineErrors as le (le)}<p class="field-error">{le}</p>{/each}
	{#if !lineErrors.length}<FormError {error} />{/if}
	{#if saved}<p class="success" role="status">{t('app.common.saved')}</p>{/if}
	{#if !disabled}<button class="btn" type="button" onclick={save}>{saveLabel}</button>{/if}
</div>
