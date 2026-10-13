<script lang="ts">
	import { page } from '$app/state';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import Play from '@lucide/svelte/icons/play';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import Workflow from '@lucide/svelte/icons/workflow';
	import { Button } from '$lib/components/ui/button';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import MiniFlow from '$lib/components/MiniFlow.svelte';
	import { api } from '$lib/api';
	import { errorMessage } from '$lib/state.svelte';
	import { SUPPORT, laidOut, usePattern } from '$lib/patterns';
	import type { Pattern } from '$lib/types';

	// One pattern: the example in the designer on the left, and how to build
	// it on the right. Pointing at a step in the instructions outlines it.
	const id = $derived(page.params.id ?? '');
	let pattern = $state<Pattern | null>(null);
	let related = $state<Pattern[]>([]);
	let error = $state('');
	let highlight = $state<string | undefined>();
	let pinned = $state<string | undefined>();
	let busy = $state(false);

	$effect(() => {
		pattern = null;
		error = '';
		highlight = pinned = undefined;
		const want = id;
		api.patterns()
			.then((res) => {
				const p = res.patterns.find((x) => x.id === want);
				if (!p) throw new Error('no such pattern');
				pattern = p.flow ? { ...p, flow: laidOut(p.flow) } : p;
				related = (p.related ?? []).map((r) => res.patterns.find((x) => x.id === r)).filter((x): x is Pattern => !!x);
			})
			.catch((e) => (error = errorMessage(e)));
	});

	const support = $derived(pattern ? SUPPORT[pattern.support] : null);
	const nodeLabel = (nodeId?: string) => pattern?.flow?.nodes.find((n) => n.id === nodeId)?.label;

	async function open() {
		if (!pattern) return;
		busy = true;
		await usePattern(pattern);
		busy = false;
	}
</script>

<svelte:head><title>{pattern?.name ?? 'Pattern'} · Design patterns · Visual Workflows</title></svelte:head>

<div class="flex h-full flex-col">
	<header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-[var(--line)] px-6 py-4">
		<a href="/patterns" class="mono flex items-center gap-1 text-xs text-[var(--slate-300)]"><ChevronLeft class="size-4" /> Patterns</a>
		<div class="min-w-0 flex-1">
			<div class="eyebrow">{pattern?.category ?? 'Design pattern'}</div>
			<div class="flex flex-wrap items-center gap-3">
				<h1 class="text-xl font-light">{pattern?.name ?? (error ? 'Pattern not found' : 'Loading…')}</h1>
				{#if support}<span class="status-badge" style:color={support.color} title={support.hint}>{support.label}</span>{/if}
			</div>
			{#if pattern}<p class="mt-0.5 text-sm text-[var(--indigo-100)]">{pattern.summary}</p>{/if}
		</div>
		{#if pattern}
			<a href={pattern.docsUrl} target="_blank" rel="noreferrer" class="mono flex items-center gap-1 text-xs">Temporal docs <ExternalLink class="size-3" /></a>
			{#if pattern.flow}
				<Button class="btn-uv" disabled={busy} onclick={open}><Sparkles /> Open as a new flow</Button>
			{/if}
		{/if}
	</header>

	{#if pattern}
		<div class="flex min-h-0 flex-1">
			{#if pattern.flow}
				<div class="relative min-w-0 flex-1 bg-[var(--canvas)]">
					<MiniFlow flow={pattern.flow} highlight={pinned ?? highlight} />
					<div class="mono pointer-events-none absolute top-3 left-3 border border-[var(--line)] bg-[#111218]/90 px-2 py-1 text-[0.62rem] text-[var(--slate-300)]">
						{pattern.flow.name}{pattern.children?.length ? ` · runs ${pattern.children.map((c) => c.name).join(', ')}` : ''}
					</div>
				</div>
			{/if}

			<aside class="flex min-h-0 flex-col border-l border-[var(--line)] {pattern.flow ? 'w-[440px] shrink-0' : 'mx-auto w-full max-w-3xl border-r'}">
				<ScrollArea class="min-h-0 flex-1">
					<div class="space-y-6 p-5">
						<section class="space-y-2">
							<div class="eyebrow">Use it for</div>
							<ul class="space-y-1.5 text-sm leading-relaxed">
								{#each pattern.useFor as u, i (i)}<li class="flex gap-2"><span class="text-[var(--green)]">›</span><span>{u}</span></li>{/each}
							</ul>
						</section>

						<section class="space-y-2">
							<div class="eyebrow">{pattern.support === 'visual' ? 'How to build it' : pattern.support === 'built-in' ? 'How you get it' : 'Where this lives'}</div>
							<ol class="space-y-1">
								{#each pattern.build as step, i (i)}
									{@const label = nodeLabel(step.node)}
									<li>
										<button
											class="flex w-full gap-3 border border-transparent p-2 text-left text-sm leading-relaxed transition-colors {step.node ? 'hover:border-[var(--line)] hover:bg-[var(--accent)]' : 'cursor-default'}"
											class:pinned={step.node && pinned === step.node}
											onmouseenter={() => step.node && (highlight = step.node)}
											onmouseleave={() => (highlight = undefined)}
											onclick={() => step.node && (pinned = pinned === step.node ? undefined : step.node)}
										>
											<span class="mono grid size-5 shrink-0 place-items-center border border-[var(--line)] text-[0.62rem] text-[var(--indigo-200)]">{i + 1}</span>
											<span class="min-w-0">
												{#if label}<span class="mono mr-1 border border-[var(--uv-light)]/60 px-1 text-[0.6rem] text-[var(--uv-light)] uppercase">{label}</span>{/if}
												{step.text}
											</span>
										</button>
									</li>
								{/each}
							</ol>
						</section>

						{#if pattern.watch?.length}
							<section class="space-y-2">
								<div class="eyebrow flex items-center gap-1.5 !text-[var(--yellow)]"><TriangleAlert class="size-3.5" /> Watch for</div>
								<ul class="space-y-1.5 text-sm leading-relaxed text-[var(--indigo-100)]">
									{#each pattern.watch as w, i (i)}<li class="flex gap-2"><span class="text-[var(--yellow)]">!</span><span>{w}</span></li>{/each}
								</ul>
							</section>
						{/if}

						{#if pattern.tryIt}
							<section class="space-y-2 border border-[var(--green)]/40 bg-[var(--green)]/5 p-3">
								<div class="eyebrow flex items-center gap-1.5 !text-[var(--green)]"><Play class="size-3.5" /> Try it</div>
								<p class="text-sm leading-relaxed">{pattern.tryIt}</p>
								{#if pattern.flow}
									<Button size="sm" class="btn-uv" disabled={busy} onclick={open}><Sparkles /> Open as a new flow</Button>
								{/if}
							</section>
						{/if}

						{#if related.length}
							<section class="space-y-2">
								<div class="eyebrow">Related patterns</div>
								<div class="flex flex-wrap gap-1.5">
									{#each related as r (r.id)}
										<a href="/patterns/{r.id}" class="mono flex items-center gap-1 border border-[var(--line)] px-2 py-1 text-[0.68rem] hover:border-[var(--uv-light)]">
											<Workflow class="size-3" />{r.name}
										</a>
									{/each}
								</div>
							</section>
						{/if}
					</div>
				</ScrollArea>
			</aside>
		</div>
	{:else if error}
		<div class="grid flex-1 place-items-center text-sm text-[var(--slate-300)]">Couldn't open this pattern: {error}</div>
	{/if}
</div>

<style>
	.pinned {
		border-color: var(--uv-light) !important;
		background: rgba(68, 76, 231, 0.14);
	}
</style>
