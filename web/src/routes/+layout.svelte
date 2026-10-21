<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { Toaster } from '$lib/components/ui/sonner';
	import AppNav from '$lib/components/AppNav.svelte';
	import ConfirmHost from '$lib/components/ConfirmHost.svelte';
	import TooltipHost from '$lib/components/TooltipHost.svelte';
	import { shell } from '$lib/shell.svelte';

	let { children } = $props();
	onMount(() => shell.start());

	// Focus mode in the editor hides the platform nav too, for the widest canvas.
	const hideNav = $derived(shell.navCollapsed && page.url.pathname.startsWith('/workflows/'));
</script>

<Toaster position="bottom-right" richColors closeButton />
<ConfirmHost />
<TooltipHost />
<div class="flex h-screen">
	{#if !hideNav}<AppNav />{/if}
	<div class="relative flex min-w-0 flex-1 flex-col">
		{@render children()}
	</div>
</div>
