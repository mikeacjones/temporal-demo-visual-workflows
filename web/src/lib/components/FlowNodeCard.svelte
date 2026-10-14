<script lang="ts" module>
	import type { Node } from '@xyflow/svelte';
	import type { Direction, FlowNode } from '$lib/types';

	export type CardData = { node: FlowNode; run: boolean; direction: Direction };
	export type CardNode = Node<CardData, 'step'>;
</script>

<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { NODE_SPECS } from '$lib/nodes';
	import { handlesFor, isEntry } from '$lib/types';
	import { app } from '$lib/state.svelte';
	import { nodeTone, waitingText } from '$lib/status';

	let { id, data, selected }: NodeProps<CardNode> = $props();

	const spec = $derived(NODE_SPECS[data.node.type]);
	const handles = $derived(handlesFor(data.node));
	const main = $derived(handles[0]);
	const secondary = $derived(handles.slice(1));
	const lr = $derived(data.direction !== 'TB');
	const problems = $derived(data.run ? [] : app.problemsFor(id));
	const rs = $derived(data.run ? app.nodeRunState(id) : undefined);
	const pending = $derived(data.run ? app.pendingActivity(id) : undefined);
	const retrying = $derived(!!pending && pending.attempt > 1);
	const tone = $derived(nodeTone(rs?.status, retrying));
	const Icon = $derived(spec.icon);
	const entry = $derived(isEntry(data.node.type));
	const lane = $derived(data.node.type === 'onSignal' || data.node.type === 'onUpdate' || data.node.type === 'onStart');
	// Message lanes count what they handled; On start runs once.
	const counts = $derived(lane && data.node.type !== 'onStart');

	// Secondary outcomes sit on the perpendicular side so unhappy paths branch
	// away from the main line: LR → bottom edge, TB → right edge.
	const secondaryPos = (i: number) => (secondary.length === 1 ? 62 : 38 + (i * 44) / (secondary.length - 1));
	const undoColor = (u?: string) => (u === 'failed' ? 'var(--red)' : u === 'running' ? 'var(--pink)' : 'var(--indigo-200)');
</script>

<div
	class="relative w-[248px] border bg-[#15171f]/95 text-left shadow-[0_8px_24px_-12px_rgba(0,0,0,.8)] transition-colors"
	class:pulse={tone.pulse}
	class:opacity-50={rs?.status === 'skipped'}
	style:border-color={problems.length ? 'var(--red)' : !rs && selected ? 'var(--uv-light)' : lane && !rs ? 'color-mix(in srgb, var(--purple) 55%, #2b3348)' : tone.border}
	style:outline={selected ? '2px solid var(--uv-light)' : 'none'}
	style:outline-offset="3px"
	style:--pulse-color={tone.glow}
	style:border-style={rs?.status === 'skipped' ? 'dashed' : 'solid'}
>
	{#if !entry}
		<Handle type="target" position={lr ? Position.Left : Position.Top} class="!h-3 !w-3 !rounded-none !border-2" />
	{/if}

	<div class="flex items-start gap-2.5 px-3 pt-2.5 pb-2">
		<div
			class="mt-0.5 grid size-8 shrink-0 place-items-center border"
			style:color={spec.accent}
			style:border-color={spec.accent}
			style:background="color-mix(in srgb, {spec.accent} 12%, transparent)"
		>
			<Icon class="size-4" />
		</div>
		<div class="min-w-0 flex-1">
			<div class="flex items-center justify-between gap-2">
				<span class="eyebrow truncate !text-[0.6rem]">{spec.label}</span>
				{#if rs}
					<span class="status-badge !text-[0.55rem]" style:color={tone.text}>{tone.label}</span>
				{/if}
			</div>
			<div class="truncate text-[0.95rem] leading-tight font-semibold text-[var(--off-white)]">{data.node.label}</div>
			<div class="mono mt-0.5 truncate text-[0.68rem] text-[var(--slate-300)]">{spec.summary(data.node.config)}</div>
		</div>
	</div>

	{#if rs && ((counts && rs.invocations) || rs.undo || rs.progress || rs.childWorkflowId)}
		<div class="flex flex-wrap items-center gap-1.5 border-t border-[var(--line)] px-3 py-1 text-[0.64rem]">
			{#if counts && rs.invocations}
				<span class="mono text-[var(--purple)]">×{rs.invocations} {data.node.type === 'onUpdate' ? 'updates handled' : 'events handled'}</span>
			{/if}
			{#if rs.progress}<span class="mono text-[var(--yellow)]">{rs.progress}</span>{/if}
			{#if rs.undo}
				<span class="status-badge flex items-center gap-1 !text-[0.55rem]" style:color={undoColor(rs.undo)}
					><Undo2 class="size-2.5" />undo {rs.undo}</span
				>
			{/if}
			{#if rs.childWorkflowId}
				<a
					href="/executions/{encodeURIComponent(rs.childWorkflowId)}"
					class="nodrag mono ml-auto flex items-center gap-0.5 text-[var(--indigo-200)]"
					onclick={(e) => e.stopPropagation()}
					title={rs.childWorkflowId}>child run <ExternalLink class="size-2.5" /></a
				>
			{/if}
		</div>
	{/if}

	{#if retrying && pending}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--pink)]">
			<span class="mono font-semibold">Attempt {pending.attempt}{pending.maxAttempts ? ` of ${pending.maxAttempts}` : ''}</span>
			· Temporal will retry
			{#if pending.lastFailure}
				<div class="mono truncate text-[var(--slate-300)]" title={pending.lastFailure}>{pending.lastFailure}</div>
			{/if}
		</div>
	{:else if rs?.status === 'waiting' && rs.waiting}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--indigo-100)]">
			{waitingText(rs.waiting, (data.node.config as { condition?: string }).condition)}
		</div>
	{:else if rs?.status === 'failed' && rs.error}
		<div class="mono truncate border-t border-[var(--line)] px-3 py-1.5 text-[0.66rem] text-[var(--red)]" title={rs.error}>
			{rs.error}
		</div>
	{:else if problems.length}
		<div class="border-t border-[var(--line)] px-3 py-1.5 text-[0.68rem] leading-snug text-[var(--red)]">{problems[0].message}</div>
	{/if}

	<div
		class="mono flex items-center justify-between gap-1 overflow-hidden border-t border-[var(--line)] px-3 py-1 text-[0.6rem] whitespace-nowrap text-[var(--slate-500)]"
	>
		<span class="min-w-0 truncate">steps.{data.node.key}</span>
		<span class="ml-2 max-w-[55%] shrink-0 truncate">{spec.primitive}</span>
	</div>

	<!-- Main outcome: continues the line. End run has none. -->
	{#if main}
		<Handle
			type="source"
			position={lr ? Position.Right : Position.Bottom}
			id={main}
			class="!h-3 !w-3 !rounded-none !border-2 {rs?.handle === main ? '!border-[var(--green)] !bg-[var(--green)]' : ''}"
		/>
	{/if}
	{#if handles.length > 1}
		<span
			class="edge-handle-label pointer-events-none absolute z-10 bg-[var(--canvas)] px-0.5 leading-[1.35] whitespace-nowrap text-[var(--slate-300)]"
			style={lr ? 'left: calc(100% + 2px); top: calc(50% - 15px)' : 'left: calc(50% + 9px); top: calc(100% + 2px)'}
			style:color={rs?.handle === main ? 'var(--green)' : undefined}>{main}</span
		>
	{/if}

	<!-- Secondary outcomes: branch off perpendicular to the main line. -->
	{#each secondary as h, i (h)}
		{@const at = secondaryPos(i)}
		<Handle
			type="source"
			position={lr ? Position.Bottom : Position.Right}
			id={h}
			style={lr ? `left: ${at}%` : `top: ${at}%`}
			class="!h-3 !w-3 !rounded-none !border-2 !border-[color-mix(in_srgb,var(--pink)_70%,#465a78)] {rs?.handle === h
				? '!border-[var(--pink)] !bg-[var(--pink)]'
				: ''}"
		/>
		<span
			class="edge-handle-label pointer-events-none absolute z-10 bg-[var(--canvas)] px-0.5 leading-[1.35] whitespace-nowrap"
			style={lr ? `left: calc(${at}% + 9px); top: calc(100% + 2px)` : `left: calc(100% + 2px); top: calc(${at}% - 15px)`}
			style:color={rs?.handle === h ? 'var(--pink)' : 'color-mix(in srgb, var(--pink) 60%, var(--slate-300))'}>{h}</span
		>
	{/each}
</div>
