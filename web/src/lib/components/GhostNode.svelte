<script lang="ts">
	import { Handle, Position, type NodeProps, type Node } from '@xyflow/svelte';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { app } from '$lib/state.svelte';

	// A built-in rollback, drawn as a ghost under its member: covered without
	// a step of its own. Select it to customize it into a real step.
	type GhostData = { label: string; note: string; member: string; undo?: string; direction?: 'LR' | 'TB' };
	let { data, selected }: NodeProps<Node<GhostData, 'ghost'>> = $props();
	// In a run, the rollback's progress follows the live state.
	const undo = $derived(app.mode === 'runs' ? app.shownState?.nodes[data.member]?.undo : undefined);
	const color = $derived(undo === 'failed' ? 'var(--red)' : undo === 'done' ? 'var(--green)' : undo === 'running' ? 'var(--pink)' : 'color-mix(in srgb, var(--pink) 70%, var(--slate-300))');
</script>

<div
	class="w-[200px] border border-dashed bg-[#15171f]/60 px-2.5 py-1.5"
	style:border-color={color}
	style:outline={selected ? '2px solid var(--uv-light)' : 'none'}
	style:outline-offset="3px"
	data-tip={data.note}
>
	<!-- The link arrives from the member, across the track. -->
	<Handle type="target" position={data.direction === 'TB' ? Position.Left : Position.Top} class="!h-2 !w-2 !rounded-none !border !opacity-0" />
	<div class="flex items-center gap-1.5 text-[0.74rem]" style:color={color}>
		<Undo2 class="size-3 shrink-0" />
		<span class="truncate font-semibold">{data.label}</span>
	</div>
	<div class="mono text-[0.58rem] text-[var(--slate-500)]">built in{undo ? ` · undo ${undo}` : app.mode === 'runs' ? '' : ' · select to customize'}</div>
</div>
