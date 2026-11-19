<script lang="ts">
	import { fieldText, t } from '#lib/i18n.svelte.ts';

	let {
		id,
		label,
		value = $bindable(''),
		type = 'text',
		error = '',
		hint = '',
		optional = false,
		autocomplete = undefined,
		inputmode = undefined,
		maxlength = undefined,
		rows = 0,
		placeholder = undefined
	}: {
		id: string;
		label: string;
		value?: string;
		type?: string;
		error?: string;
		hint?: string;
		optional?: boolean;
		autocomplete?: AutoFill;
		inputmode?: 'numeric' | 'tel' | 'email' | 'text';
		maxlength?: number;
		rows?: number;
		placeholder?: string;
	} = $props();
</script>

<div class="field">
	<label for={id}>{label}{#if optional}<span class="muted optional">({t('app.common.optional')})</span>{/if}</label>
	{#if rows > 0}
		<textarea
			{id}
			{rows}
			{maxlength}
			{placeholder}
			bind:value
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? id + '-err' : hint ? id + '-hint' : undefined}
		></textarea>
	{:else}
		<input
			{id}
			{type}
			{autocomplete}
			{inputmode}
			{maxlength}
			{placeholder}
			bind:value
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? id + '-err' : hint ? id + '-hint' : undefined}
		/>
	{/if}
	{#if hint && !error}<p class="hint" id={id + '-hint'}>{hint}</p>{/if}
	{#if error}<p class="field-error" id={id + '-err'}>{fieldText(error)}</p>{/if}
</div>
