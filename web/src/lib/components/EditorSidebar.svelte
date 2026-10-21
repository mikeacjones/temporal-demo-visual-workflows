<script lang="ts">
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { app } from '$lib/state.svelte';
	import { NODE_SPECS, PALETTE_GROUPS } from '$lib/nodes';
	import { ago, executionTone } from '$lib/status';
	import { SINGLE_TYPES, type NodeType } from '$lib/types';

	function dragStart(e: DragEvent, type: NodeType) {
		e.dataTransfer?.setData('application/x-flow-node', type);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy';
	}

	// A flow has one trigger and at most one On start.
	const taken = (type: NodeType) => SINGLE_TYPES.includes(type) && !!app.flow?.nodes.some((n) => n.type === type);
	const shortId = (wid: string) => wid.split('-').slice(-2).join('-');
</script>

{#snippet edgeToggle()}
	<button
		class="absolute top-1/2 -right-3 z-20 grid h-10 w-3 -translate-y-1/2 place-items-center border border-[var(--line)] bg-[#1b1d29] text-[var(--slate-300)] hover:border-[var(--uv-light)] hover:text-[var(--off-white)]"
		onclick={() => (app.leftCollapsed = !app.leftCollapsed)}
		data-tip={app.leftCollapsed ? 'Show panel ( [ )' : 'Hide panel ( [ )'}
		aria-label={app.leftCollapsed ? 'Show panel' : 'Hide panel'}
	>
		{#if app.leftCollapsed}<ChevronRight class="size-3" />{:else}<ChevronLeft class="size-3" />{/if}
	</button>
{/snippet}

{#if app.leftCollapsed}
	<aside class="relative flex h-full w-12 shrink-0 flex-col items-center gap-1 border-r border-[var(--line)] bg-[#111218]/85 py-3">
		{@render edgeToggle()}
		{#if app.mode === 'build'}
			{#each PALETTE_GROUPS.flatMap((g) => g.types) as type (type)}
				{@const spec = NODE_SPECS[type]}
				{@const Icon = spec.icon}
				{@const disabled = !app.flow || taken(type)}
				<button
					class="grid size-8 shrink-0 place-items-center border disabled:opacity-30"
					style:color={spec.accent}
					style:border-color={spec.accent}
					style:background="color-mix(in srgb, {spec.accent} 10%, transparent)"
					draggable={!disabled}
					ondragstart={(e) => dragStart(e, type)}
					onclick={() => app.requestAdd(type)}
					{disabled}
					data-tip="{spec.label} — {spec.primitive}"
					aria-label="Add {spec.label}"
				>
					<Icon class="size-3.5" />
				</button>
			{/each}
		{:else}
			<span class="mono mb-1 text-[0.55rem] text-[var(--slate-500)] uppercase">Runs</span>
			<div class="flex min-h-0 flex-col items-center gap-1.5 overflow-y-auto">
				{#each app.runs as r (r.workflowId)}
					{@const tone = executionTone(r.status)}
					<button
						class="grid size-7 shrink-0 place-items-center border"
						style:border-color={app.selectedRunId === r.workflowId ? 'var(--green)' : 'var(--line)'}
						onclick={() => app.openRun(r.workflowId)}
						data-tip="{shortId(r.workflowId)}{r.key ? ` · ${r.key}` : ''} · {tone.label} · {ago(r.startTime)}"
						aria-label="Open run {shortId(r.workflowId)}, {tone.label}"
					>
						<span class="inline-block size-2.5" style:background={tone.text}></span>
					</button>
				{/each}
			</div>
		{/if}
	</aside>
{:else}
	<aside class="relative flex h-full w-[248px] shrink-0 flex-col border-r border-[var(--line)] bg-[#111218]/85">
		{@render edgeToggle()}
		{#if app.mode === 'build'}
			<div class="px-4 pt-3 pb-1">
				<span class="eyebrow">Steps</span>
				<p class="mt-0.5 text-[0.68rem] text-[var(--slate-300)]">Drag onto the canvas, or click to add.</p>
			</div>
			<ScrollArea class="min-h-0 flex-1 px-3 pb-3">
				{#each PALETTE_GROUPS as g (g.name)}
					<div class="mono mt-3 mb-1 px-1 text-[0.6rem] tracking-widest text-[var(--slate-500)] uppercase">{g.name}</div>
					{#each g.types as type (type)}
						{@const spec = NODE_SPECS[type]}
						{@const Icon = spec.icon}
						{@const disabled = !app.flow || taken(type)}
						<button
							class="mb-1.5 flex w-full items-center gap-2.5 border border-[var(--line)] bg-[#15171f] px-2.5 py-1.5 text-left transition-colors hover:border-[var(--uv-light)] disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-[var(--line)]"
							draggable={!disabled}
							ondragstart={(e) => dragStart(e, type)}
							onclick={() => app.requestAdd(type)}
							{disabled}
							data-tip={taken(type) ? `A flow has only one ${spec.label}` : `Add ${spec.label}`}
						>
							<span
								class="grid size-7 shrink-0 place-items-center border"
								style:color={spec.accent}
								style:border-color={spec.accent}
								style:background="color-mix(in srgb, {spec.accent} 12%, transparent)"
							>
								<Icon class="size-3.5" />
							</span>
							<span class="min-w-0">
								<span class="block text-[0.82rem] font-semibold">{spec.label}</span>
								<span class="mono block truncate text-[0.6rem] text-[var(--indigo-200)]" data-tip={spec.primitive}>{spec.primitive}</span>
							</span>
						</button>
					{/each}
				{/each}
			</ScrollArea>
		{:else}
			<div class="flex items-center justify-between px-4 pt-3 pb-1">
				<span class="eyebrow">Recent runs</span>
				<span class="mono text-[0.6rem] text-[var(--slate-500)]">{app.runs.length}</span>
			</div>
			<ScrollArea class="min-h-0 flex-1 px-2 pb-3">
				{#each app.runs as r (r.workflowId)}
					{@const tone = executionTone(r.status)}
					<button
						class="flex w-full items-center gap-2 border-l-2 px-2 py-2 text-left hover:bg-[var(--accent)]"
						style:border-color={app.selectedRunId === r.workflowId ? 'var(--green)' : 'transparent'}
						style:background={app.selectedRunId === r.workflowId ? 'rgba(68,76,231,.14)' : undefined}
						onclick={() => app.openRun(r.workflowId)}
					>
						<span class="inline-block size-2 shrink-0" style:background={tone.text}></span>
						<span class="min-w-0 flex-1">
							<span class="mono block truncate text-[0.68rem]" data-tip={r.workflowId}>{r.key ? `key ${r.key}` : shortId(r.workflowId)}</span>
							<span class="block text-[0.62rem] text-[var(--slate-300)]">{tone.label} · {r.source ?? 'run'} · {ago(r.startTime)}</span>
						</span>
					</button>
				{:else}
					<p class="px-2 py-2 text-xs text-[var(--slate-300)]">No runs yet. Switch to Build and press Run.</p>
				{/each}
			</ScrollArea>
		{/if}
	</aside>
{/if}
