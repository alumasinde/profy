<script lang="ts">
	import { page } from '$app/state';
	import LocationDetail from '#lib/components/LocationDetail.svelte';
</script>

{#key page.params.id}<LocationDetail type="county" id={page.params.id ?? ''} />{/key}
