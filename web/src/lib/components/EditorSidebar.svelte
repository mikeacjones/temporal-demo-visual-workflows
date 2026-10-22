<script lang="ts">
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import Search from '@lucide/svelte/icons/search';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { app } from '$lib/state.svelte';
	import { NODE_SPECS, PALETTE_GROUPS } from '$lib/nodes';
	import { ago, executionTone } from '$lib/status';
	import { connectorSpec } from '$lib/connectors';
	import { SINGLE_TYPES, isConnector, type ConnectorType, type NodeType } from '$lib/types';

	// A connector's operations are palette items of their own: the payload is
	// type:operation.
	function dragStart(e: DragEvent, type: NodeType, variant?: string) {
		e.dataTransfer?.setData('application/x-flow-node', variant ? `${type}:${variant}` : type);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy';
	}

	let query = $state('');
	let open = $state<Record<string, boolean>>({});
	const q = $derived(query.trim().toLowerCase());
	const matches = (...texts: string[]) => !q || texts.some((t) => t.toLowerCase().includes(q));
	const operations = (type: ConnectorType) => {
		const spec = connectorSpec(type);
		return (spec?.operations ?? []).filter((o) => matches(spec!.label, o.label, o.activity));
	};
	const groupItems = (types: NodeType[]) =>
		types.filter((t) => (isConnector(t) ? operations(t).length > 0 : matches(NODE_SPECS[t].label, NODE_SPECS[t].primitive)));

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
					onclick={() => (isConnector(type) ? ((open[type] = true), (app.leftCollapsed = false)) : app.requestAdd(type))}
					{disabled}
					data-tip="{spec.label} — {isConnector(type) ? 'its operations' : spec.primitive}"
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
				<label class="mt-2 flex items-center gap-1.5 border border-[var(--line)] bg-[#15171f] px-2 py-1 focus-within:border-[var(--uv-light)]">
					<Search class="size-3 shrink-0 text-[var(--slate-500)]" />
					<input class="w-full bg-transparent text-xs outline-none placeholder:text-[var(--slate-500)]" placeholder="Search steps and operations" bind:value={query} />
				</label>
			</div>
			<ScrollArea class="min-h-0 flex-1 px-3 pb-3">
				{#each PALETTE_GROUPS as g (g.name)}
					{@const types = groupItems(g.types)}
					{#if types.length}
						<div class="mono mt-3 mb-1 px-1 text-[0.6rem] tracking-widest text-[var(--slate-500)] uppercase">{g.name}</div>
					{/if}
					{#each types as type (type)}
						{@const spec = NODE_SPECS[type]}
						{@const Icon = spec.icon}
						{@const disabled = !app.flow || taken(type)}
						{#if isConnector(type)}
							{@const ops = operations(type)}
							{@const expanded = !!q || !!open[type]}
							<div class="mb-1.5 border border-[var(--line)] bg-[#15171f]">
								<button class="flex w-full items-center gap-2.5 px-2.5 py-1.5 text-left hover:bg-[var(--accent)]" onclick={() => (open[type] = !open[type])} aria-expanded={expanded}>
									<span
										class="grid size-7 shrink-0 place-items-center border"
										style:color={spec.accent}
										style:border-color={spec.accent}
										style:background="color-mix(in srgb, {spec.accent} 12%, transparent)"
									>
										<Icon class="size-3.5" />
									</span>
									<span class="min-w-0 flex-1">
										<span class="block text-[0.82rem] font-semibold">{spec.label}</span>
										<span class="mono block text-[0.6rem] text-[var(--indigo-200)]">{ops.length} operations</span>
									</span>
									{#if expanded}<ChevronDown class="size-3.5 text-[var(--slate-300)]" />{:else}<ChevronRight class="size-3.5 text-[var(--slate-300)]" />{/if}
								</button>
								{#if expanded}
									<div class="border-t border-[var(--line)] py-1">
										{#each ops as op (op.name)}
											<button
												class="flex w-full items-center gap-2 py-1 pr-2 pl-4 text-left hover:bg-[var(--accent)] disabled:opacity-40"
												draggable={!disabled}
												ondragstart={(e) => dragStart(e, type, op.name)}
												onclick={() => app.requestAdd(type, op.name)}
												{disabled}
												data-tip="Runs as the {op.activity} Activity"
											>
												<span class="inline-block size-1.5 shrink-0" style:background={spec.accent}></span>
												<span class="truncate text-[0.74rem]">{op.label}</span>
											</button>
										{/each}
									</div>
								{/if}
							</div>
						{:else}
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
						{/if}
					{/each}
				{/each}
				{#if q && PALETTE_GROUPS.every((g) => !groupItems(g.types).length)}
					<p class="px-1 py-3 text-xs text-[var(--slate-300)]">Nothing matches "{query}".</p>
				{/if}
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
