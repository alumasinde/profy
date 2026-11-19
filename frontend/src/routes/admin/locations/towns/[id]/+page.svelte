<script lang="ts">
	import { page } from '$app/state';
	import LocationDetail from '#lib/components/LocationDetail.svelte';
</script>

{#key page.params.id}<LocationDetail type="town" id={page.params.id ?? ''} />{/key}
