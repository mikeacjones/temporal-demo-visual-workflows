<script lang="ts">
	import { onMount } from 'svelte';
	import Eye from '@lucide/svelte/icons/eye';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import { toast } from 'svelte-sonner';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import Starfield from '$lib/components/Starfield.svelte';
	import MiniGraph from '$lib/components/MiniGraph.svelte';
	import MiniFlow from '$lib/components/MiniFlow.svelte';
	import ZiggyDrift from '$lib/components/ZiggyDrift.svelte';
	import WorkerPill from '$lib/components/WorkerPill.svelte';
	import { api } from '$lib/api';
	import { errorMessage, normalizeFlow } from '$lib/state.svelte';
	import { useTemplate } from '$lib/templates';
	import { ago } from '$lib/status';
	import type { Template } from '$lib/types';

	let templates = $state<Template[]>([]);
	let loaded = $state(false);
	let preview = $state<Template | null>(null);

	async function load() {
		try {
			templates = ((await api.templates()) ?? []).map((t) => ({ ...t, flow: normalizeFlow(t.flow) }));
		} catch (err) {
			toast.error(`Could not load templates: ${errorMessage(err)}`);
		} finally {
			loaded = true;
		}
	}
	onMount(load);

	async function unpublish(t: Template) {
		if (!confirm(`Unpublish "${t.name}"? Copies people already made are unaffected.`)) return;
		try {
			await api.deleteTemplate(t.id);
			await load();
			toast.success('Unpublished');
		} catch (err) {
			toast.error(errorMessage(err));
		}
	}
	const builtIn = $derived(templates.filter((t) => t.builtIn));
	const community = $derived(templates.filter((t) => !t.builtIn));
</script>

<svelte:head><title>Templates · Visual Workflows</title></svelte:head>

{#snippet card(t: Template)}
	<article class="group flex flex-col border border-[var(--line)] bg-[#14161e]/90 transition-colors hover:border-[var(--uv-light)]">
		<button class="block border-b border-[var(--line)] bg-[var(--canvas)] p-3" onclick={() => (preview = t)} aria-label="Preview {t.name}">
			<MiniGraph flow={t.flow} height={120} />
		</button>
		<div class="flex flex-1 flex-col gap-1.5 p-4">
			<div class="flex items-center gap-2">
				<h3 class="min-w-0 flex-1 truncate font-semibold">{t.name}</h3>
				{#if t.builtIn}<span class="status-badge text-[var(--green)]">built-in</span>{/if}
				{#if t.mine}<span class="status-badge text-[var(--purple)]">mine</span>{/if}
				{#if !t.listed}<span class="status-badge text-[var(--slate-300)]" title="Only people with the share link can see it">link only</span>{/if}
			</div>
			{#if t.description}<p class="line-clamp-3 text-xs leading-relaxed text-[var(--slate-300)]">{t.description}</p>{/if}
			<div class="mono mt-auto pt-2 text-[0.62rem] text-[var(--slate-500)]">
				{t.nodeCount} steps{t.author ? ` · by ${t.author}` : ''}{t.publishedAt ? ` · ${ago(t.publishedAt)}` : ''}
			</div>
			<div class="flex items-center gap-2 pt-1">
				<Button size="sm" class="btn-uv" onclick={() => useTemplate(t.id)}><Sparkles /> Use template</Button>
				<Button size="sm" variant="ghost" class="mono" onclick={() => (preview = t)}><Eye /> Preview</Button>
				{#if t.mine && !t.builtIn}
					<Button size="icon-sm" variant="ghost" class="ml-auto" title="Unpublish" onclick={() => unpublish(t)}><Trash2 /></Button>
				{/if}
			</div>
		</div>
	</article>
{/snippet}

<div class="h-full overflow-y-auto">
	<section class="relative overflow-hidden border-b border-[var(--line)]">
		<Starfield />
		<ZiggyDrift count={5} />
		<div class="relative flex flex-wrap items-end gap-6 px-8 pt-8 pb-7">
			<div class="max-w-2xl">
				<div class="eyebrow">Templates</div>
				<h1 class="mt-2 text-[2.3rem] leading-[1.05] font-light tracking-[-0.04em]">Start from a flow <span class="text-gradient-uv">that already works.</span></h1>
				<p class="mt-2 text-sm text-[var(--slate-300)]">Using a template gives you your own copy to edit. Publish yours from the editor's Share menu.</p>
			</div>
			<div class="ml-auto"><WorkerPill /></div>
		</div>
	</section>

	<div class="space-y-8 px-8 py-6">
		{#if builtIn.length}
			<section>
				<div class="eyebrow mb-3">Built-in examples</div>
				<div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
					{#each builtIn as t (t.id)}{@render card(t)}{/each}
				</div>
			</section>
		{/if}
		<section>
			<div class="eyebrow mb-3">Published by people here</div>
			{#if community.length}
				<div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
					{#each community as t (t.id)}{@render card(t)}{/each}
				</div>
			{:else if loaded}
				<p class="border border-dashed border-[var(--line)] px-6 py-6 text-sm text-[var(--slate-300)]">Nothing published yet. Open a workflow and choose Share → Publish to gallery.</p>
			{/if}
		</section>
	</div>
</div>

<Dialog.Root open={!!preview} onOpenChange={(o) => !o && (preview = null)}>
	<Dialog.Content class="flex h-[80vh] flex-col sm:max-w-5xl">
		<Dialog.Header>
			<Dialog.Title>{preview?.name}</Dialog.Title>
			<Dialog.Description>{preview?.description}</Dialog.Description>
		</Dialog.Header>
		<div class="min-h-0 flex-1 border border-[var(--line)] bg-[var(--canvas)]">
			{#if preview}<MiniFlow flow={preview.flow} />{/if}
		</div>
		<Dialog.Footer>
			<Button variant="ghost" onclick={() => (preview = null)}>Close</Button>
			<Button class="btn-uv" onclick={() => preview && useTemplate(preview.id)}><Sparkles /> Use template</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
