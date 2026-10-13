<script lang="ts">
	import { SvelteFlow, SvelteFlowProvider, Background, BackgroundVariant, Controls, type Edge, type NodeTypes } from '@xyflow/svelte';
	import FlowNodeCard, { type CardNode } from './FlowNodeCard.svelte';
	import FitOnResize from './FitOnResize.svelte';
	import { toEdges } from '$lib/edges';
	import type { Definition } from '$lib/types';

	// A read-only canvas for previews (templates, version history).
	// highlight outlines one node, for docs that point at a step.
	let { flow, highlight }: { flow: Definition; highlight?: string } = $props();

	const nodeTypes: NodeTypes = { step: FlowNodeCard };
	let width = $state(0);
	let nodes = $state.raw<CardNode[]>([]);
	let edges = $state.raw<Edge[]>([]);

	$effect(() => {
		const direction = flow.settings?.direction ?? 'LR';
		nodes = flow.nodes.map((n) => ({
			id: n.id,
			type: 'step',
			position: { ...n.position },
			data: { node: n, run: false, direction },
			selected: n.id === highlight,
			draggable: false,
			connectable: false,
			selectable: false,
			deletable: false
		}));
		edges = toEdges(flow, null, false);
	});
</script>

<SvelteFlowProvider>
	<div class="h-full w-full" bind:clientWidth={width}>
		<SvelteFlow
			bind:nodes
			bind:edges
			{nodeTypes}
			colorMode="dark"
			minZoom={0.1}
			nodesDraggable={false}
			nodesConnectable={false}
			elementsSelectable={false}
			deleteKey={null}
		>
			<Background variant={BackgroundVariant.Dots} gap={22} size={1.2} />
			<Controls showLock={false} showFitView />
			<FitOnResize {width} fitKey={`${flow.id}:${flow.version}`} />
		</SvelteFlow>
	</div>
</SvelteFlowProvider>
