<script lang="ts">
	import { onMount } from 'svelte';
	import Search from '@lucide/svelte/icons/search';
	import Cpu from '@lucide/svelte/icons/cpu';
	import Sparkle from '@lucide/svelte/icons/sparkle';
	import { toast } from 'svelte-sonner';
	import { Input } from '$lib/components/ui/input';
	import Starfield from '$lib/components/Starfield.svelte';
	import ZiggyDrift from '$lib/components/ZiggyDrift.svelte';
	import MiniGraph from '$lib/components/MiniGraph.svelte';
	import { api } from '$lib/api';
	import { errorMessage } from '$lib/state.svelte';
	import { SUPPORT, laidOut } from '$lib/patterns';
	import type { Pattern } from '$lib/types';

	// The design pattern library: Temporal's catalog, and how each one looks in
	// the visual designer.
	let categories = $state<string[]>([]);
	let patterns = $state<Pattern[]>([]);
	let query = $state('');
	let category = $state('');
	let support = $state<Pattern['support'] | ''>('');

	onMount(async () => {
		try {
			const res = await api.patterns();
			categories = res.categories;
			patterns = res.patterns.map((p) => (p.flow ? { ...p, flow: laidOut(p.flow) } : p));
		} catch (err) {
			toast.error(`Could not load patterns: ${errorMessage(err)}`);
		}
	});

	const matches = (p: Pattern) => {
		const q = query.trim().toLowerCase();
		return (
			(!category || p.category === category) &&
			(!support || p.support === support) &&
			(!q || `${p.name} ${p.summary} ${p.useFor.join(' ')}`.toLowerCase().includes(q))
		);
	};
	const groups = $derived(categories.map((c) => ({ name: c, items: patterns.filter((p) => p.category === c && matches(p)) })).filter((g) => g.items.length));
	const counts = $derived({
		visual: patterns.filter((p) => p.support === 'visual').length,
		builtIn: patterns.filter((p) => p.support === 'built-in').length,
		total: patterns.length
	});
</script>

<svelte:head><title>Design patterns · Visual Workflows</title></svelte:head>

<div class="h-full overflow-y-auto">
	<section class="relative overflow-hidden border-b border-[var(--line)]">
		<Starfield />
		<ZiggyDrift count={5} />
		<div class="relative max-w-3xl px-8 pt-8 pb-7">
			<div class="eyebrow">Design patterns</div>
			<h1 class="mt-2 text-[2.3rem] leading-[1.05] font-light tracking-[-0.04em]">Temporal's patterns, <span class="text-gradient-uv">drawn.</span></h1>
			<p class="mt-2 text-sm text-[var(--slate-300)]">
				Every pattern in Temporal's design pattern catalog, and how to build it here. {counts.visual} you draw, {counts.builtIn} every flow gets for free, and the rest
				are worker choices, explained. Open one to see it in the designer, with how to configure it and when to use it.
			</p>
		</div>
	</section>

	<div class="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-[var(--line)] bg-[var(--background)]/95 px-8 py-3 backdrop-blur">
		<div class="relative w-64">
			<Search class="absolute top-2 left-2.5 size-4 text-[var(--slate-500)]" />
			<Input bind:value={query} placeholder="Search patterns" class="pl-8" />
		</div>
		<div class="flex flex-wrap gap-1">
			<button class="chip" class:active={!category} onclick={() => (category = '')}>All</button>
			{#each categories as c (c)}
				<button class="chip" class:active={category === c} onclick={() => (category = category === c ? '' : c)}>{c}</button>
			{/each}
		</div>
		<div class="ml-auto flex gap-1">
			{#each Object.entries(SUPPORT) as [key, s] (key)}
				<button class="chip" class:active={support === key} data-tip={s.hint} onclick={() => (support = support === key ? '' : (key as Pattern['support']))}>
					<span class="inline-block size-1.5" style:background={s.color}></span>{s.label}
				</button>
			{/each}
		</div>
	</div>

	<div class="space-y-8 px-8 py-6">
		{#each groups as g (g.name)}
			<section>
				<div class="eyebrow mb-3">{g.name}</div>
				<div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
					{#each g.items as p (p.id)}
						{@const s = SUPPORT[p.support]}
						<a href="/patterns/{p.id}" class="group flex flex-col border border-[var(--line)] bg-[#14161e]/90 transition-colors hover:border-[var(--uv-light)]">
							<div class="border-b border-[var(--line)] bg-[var(--canvas)] p-3">
								{#if p.flow}
									<MiniGraph flow={p.flow} height={110} />
								{:else}
									<div class="grid h-[110px] place-items-center text-[var(--slate-500)]">
										{#if p.support === 'built-in'}<Sparkle class="size-8" />{:else}<Cpu class="size-8" />{/if}
									</div>
								{/if}
							</div>
							<div class="flex flex-1 flex-col gap-1.5 p-4">
								<div class="flex items-center gap-2">
									<h3 class="min-w-0 flex-1 truncate font-semibold group-hover:text-[var(--off-white)]">{p.name}</h3>
									<span class="status-badge shrink-0" style:color={s.color} data-tip={s.hint}>{s.label}</span>
								</div>
								<p class="line-clamp-3 text-xs leading-relaxed text-[var(--slate-300)]">{p.summary}</p>
							</div>
						</a>
					{/each}
				</div>
			</section>
		{:else}
			{#if patterns.length}
				<p class="border border-dashed border-[var(--line)] px-6 py-6 text-sm text-[var(--slate-300)]">No pattern matches.</p>
			{/if}
		{/each}
	</div>
</div>

<style>
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		border: 1px solid var(--line);
		padding: 0.2rem 0.55rem;
		font-family: var(--font-mono);
		font-size: 0.62rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--slate-300);
	}
	.chip:hover {
		border-color: var(--uv-light);
	}
	.chip.active {
		border-color: var(--uv-light);
		background: rgba(68, 76, 231, 0.18);
		color: var(--off-white);
	}
</style>
