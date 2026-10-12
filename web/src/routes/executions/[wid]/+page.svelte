<script lang="ts">
	import { page } from '$app/state';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import RunScreen from '$lib/components/RunScreen.svelte';
	import RunInspector from '$lib/components/RunInspector.svelte';
	import WorkerPill from '$lib/components/WorkerPill.svelte';
	import { app } from '$lib/state.svelte';

	// A run on its own, reached from Executions, Overview, or a child-run link.
	const wid = $derived(decodeURIComponent(page.params.wid ?? ''));
	$effect(() => {
		app.mode = 'runs';
		app.openRun(wid);
	});
</script>

<svelte:head><title>Run · Visual Workflows</title></svelte:head>

<div class="flex h-full flex-col">
	<header class="flex h-12 shrink-0 items-center gap-3 border-b border-[var(--line)] bg-[#111218]/90 px-3">
		<a href="/executions" class="mono flex items-center gap-1 text-xs text-[var(--slate-300)] hover:text-[var(--off-white)]"><ChevronLeft class="size-4" /> Executions</a>
		{#if app.lastState}
			<a href="/workflows/{encodeURIComponent(app.lastState.flowId)}?mode=runs&run={encodeURIComponent(wid)}" class="mono text-xs">Open in editor</a>
		{/if}
		<div class="ml-auto"><WorkerPill /></div>
	</header>
	<div class="flex min-h-0 flex-1">
		<main class="relative min-w-0 flex-1 bg-[var(--canvas)]"><RunScreen showFlowName /></main>
		<aside class="w-[320px] shrink-0 border-l border-[var(--line)] bg-[#111218]/85"><RunInspector /></aside>
	</div>
</div>
