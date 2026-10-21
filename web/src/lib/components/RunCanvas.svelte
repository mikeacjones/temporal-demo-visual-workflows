<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteFlow, SvelteFlowProvider, Background, BackgroundVariant, Controls, type Edge, type NodeTypes } from '@xyflow/svelte';
	import FlowNodeCard, { type CardNode } from './FlowNodeCard.svelte';
	import FitOnResize from './FitOnResize.svelte';
	import LaneBands from './LaneBands.svelte';
	import { app } from '$lib/state.svelte';
	import { toEdges } from '$lib/edges';
	import { laneBands, type Band } from '$lib/layout';
	import type { Position } from '$lib/types';

	const nodeTypes: NodeTypes = { step: FlowNodeCard };

	let nodes = $state.raw<CardNode[]>([]);
	let width = $state(0);
	let bands = $state.raw<Band[]>([]);

	// The run draws the flow snapshot it executes, not the flow being edited.
	const flow = $derived(app.runView?.flow ?? null);
	const snapshotKey = $derived(flow ? `${app.selectedRunId}:${flow.id}:${flow.version}` : '');
	const direction = $derived(flow?.settings?.direction ?? 'LR');

	$effect(() => {
		const _k = snapshotKey;
		const f = untrack(() => flow);
		nodes = (f?.nodes ?? []).map((n) => ({
			id: n.id,
			type: 'step',
			position: { ...n.position },
			data: { node: n, run: true, direction },
			draggable: false,
			connectable: false,
			deletable: false,
			selected: n.id === untrack(() => app.selectedNodeId)
		}));
		const t = setTimeout(() => {
			if (!f) return;
			const pos = new Map<string, Position>(nodes.map((n) => [n.id, n.position]));
			const sizes = new Map(nodes.map((n) => [n.id, { width: n.measured?.width ?? 248, height: n.measured?.height ?? 96 }]));
			bands = laneBands(f, pos, sizes);
		}, 80);
		return () => clearTimeout(t);
	});

	// Edges follow each poll: green where the run went, dashed where it did
	// not, animated into whatever is running now.
	let edges = $state.raw<Edge[]>([]);
	$effect(() => {
		edges = flow ? toEdges(flow, app.shownState, false) : [];
	});
</script>

<SvelteFlowProvider>
	<div class="h-full w-full" bind:clientWidth={width}>
		<SvelteFlow
			bind:nodes
			bind:edges
			{nodeTypes}
			colorMode="dark"
			minZoom={0.2}
			maxZoom={1.8}
			nodesDraggable={false}
			nodesConnectable={false}
			deleteKey={null}
			onnodeclick={({ node }) => (app.selectedNodeId = node.id)}
			onpaneclick={() => (app.selectedNodeId = null)}
		>
			<LaneBands {bands} />
			<Background variant={BackgroundVariant.Dots} gap={22} size={1.4} />
			<Controls showLock={false} />
			<FitOnResize {width} fitKey={snapshotKey} />
		</SvelteFlow>
	</div>
</SvelteFlowProvider>
