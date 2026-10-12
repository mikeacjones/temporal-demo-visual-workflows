<script lang="ts">
	import { page } from '$app/state';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import { Button } from '$lib/components/ui/button';
	import MiniFlow from '$lib/components/MiniFlow.svelte';
	import { api } from '$lib/api';
	import { errorMessage, normalizeFlow } from '$lib/state.svelte';
	import { useTemplate } from '$lib/templates';
	import type { Template } from '$lib/types';

	// A shared template link: /templates/<id>?t=<token> (unlisted templates need the token).
	const id = $derived(page.params.id ?? '');
	const token = $derived(page.url.searchParams.get('t') ?? undefined);
	let template = $state<Template | null>(null);
	let error = $state('');

	$effect(() => {
		template = null;
		error = '';
		api.template(id, token)
			.then((t) => (template = { ...t, flow: normalizeFlow(t.flow) }))
			.catch((e) => (error = errorMessage(e)));
	});
</script>

<svelte:head><title>{template?.name ?? 'Shared template'} · Visual Workflows</title></svelte:head>

<div class="flex h-full flex-col">
	<header class="flex flex-wrap items-center gap-4 border-b border-[var(--line)] px-6 py-4">
		<a href="/templates" class="mono flex items-center gap-1 text-xs text-[var(--slate-300)]"><ChevronLeft class="size-4" /> Templates</a>
		<div class="min-w-0">
			<div class="eyebrow">Shared with you</div>
			<h1 class="text-xl font-light">{template?.name ?? (error ? 'Template unavailable' : 'Loading…')}</h1>
			{#if template?.description}<p class="text-xs text-[var(--slate-300)]">{template.description}</p>{/if}
		</div>
		{#if template}
			<span class="mono text-xs text-[var(--slate-300)]">{template.nodeCount} steps{template.author ? ` · by ${template.author}` : ''}</span>
			<Button class="btn-uv ml-auto" onclick={() => template && useTemplate(template.id, token)}><Sparkles /> Use template</Button>
		{/if}
	</header>
	<div class="min-h-0 flex-1 bg-[var(--canvas)]">
		{#if template}
			<MiniFlow flow={template.flow} />
		{:else if error}
			<div class="grid h-full place-items-center text-sm text-[var(--slate-300)]">Couldn't open that link: {error}</div>
		{/if}
	</div>
</div>
