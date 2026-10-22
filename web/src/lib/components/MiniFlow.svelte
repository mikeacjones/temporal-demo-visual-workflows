<script lang="ts">
	import { SvelteFlow, SvelteFlowProvider, Background, BackgroundVariant, Controls, type Edge, type NodeTypes, type EdgeTypes } from '@xyflow/svelte';
	import LoopExitEdge from './LoopExitEdge.svelte';
	import LoopBackEdge from './LoopBackEdge.svelte';
	import FlowNodeCard, { type CardNode } from './FlowNodeCard.svelte';
	import FitOnResize from './FitOnResize.svelte';
	import Scopes from './Scopes.svelte';
	import { toEdges } from '$lib/edges';
	import { bodyScopes, sagaScopes, type Scope } from '$lib/layout';
	import type { Definition, Position } from '$lib/types';

	// A read-only canvas for previews (templates, version history).
	// highlight outlines one node, for docs that point at a step.
	let { flow, highlight }: { flow: Definition; highlight?: string } = $props();

	const nodeTypes: NodeTypes = { step: FlowNodeCard };
	const edgeTypes: EdgeTypes = { loopExit: LoopExitEdge, loopBack: LoopBackEdge };
	let width = $state(0);
	let nodes = $state.raw<CardNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let scopes = $state.raw<Scope[]>([]);

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
		// Scope boxes need measured sizes, which exist after the next render.
		const f = flow;
		const t = setTimeout(() => {
			const pos = new Map<string, Position>(nodes.map((n) => [n.id, n.position]));
			const sizes = new Map(nodes.map((n) => [n.id, { width: n.measured?.width ?? 248, height: n.measured?.height ?? 96 }]));
			scopes = [...bodyScopes(f, pos, sizes), ...sagaScopes(f, pos, sizes)];
		}, 80);
		return () => clearTimeout(t);
	});
</script>

<SvelteFlowProvider>
	<div class="h-full w-full" bind:clientWidth={width}>
		<SvelteFlow
			bind:nodes
			bind:edges
			{nodeTypes}
			{edgeTypes}
			colorMode="dark"
			minZoom={0.1}
			nodesDraggable={false}
			nodesConnectable={false}
			elementsSelectable={false}
			deleteKey={null}
		>
			<Background variant={BackgroundVariant.Dots} gap={22} size={1.2} />
			<Scopes {scopes} />
			<Controls showLock={false} showFitView />
			<FitOnResize {width} fitKey={`${flow.id}:${flow.version}`} />
		</SvelteFlow>
	</div>
</SvelteFlowProvider>
