<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteFlow, SvelteFlowProvider, Background, BackgroundVariant, Controls, type Edge, type NodeTypes, type EdgeTypes } from '@xyflow/svelte';
	import LoopExitEdge from './LoopExitEdge.svelte';
	import LoopBackEdge from './LoopBackEdge.svelte';
	import FlowNodeCard, { type CardNode } from './FlowNodeCard.svelte';
	import GhostNode from './GhostNode.svelte';
	import { cardSagas, ghostNodes, isGhost, sagaEdges } from '$lib/sagaCanvas';
	import FitOnResize from './FitOnResize.svelte';
	import LaneBands from './LaneBands.svelte';
	import Scopes from './Scopes.svelte';
	import { app } from '$lib/state.svelte';
	import { toEdges } from '$lib/edges';
	import { bodyScopes, laneBands, sagaScopes, type Band, type Scope } from '$lib/layout';
	import type { Position } from '$lib/types';

	const nodeTypes: NodeTypes = { step: FlowNodeCard, ghost: GhostNode };
	const edgeTypes: EdgeTypes = { loopExit: LoopExitEdge, loopBack: LoopBackEdge };

	let nodes = $state.raw<CardNode[]>([]);
	let width = $state(0);
	let bands = $state.raw<Band[]>([]);
	let scopes = $state.raw<Scope[]>([]);
	const sagaStatus = $derived(Object.fromEntries(Object.entries(app.shownState?.sagas ?? {}).map(([id, st]) => [id, st.status || undefined])));

	// The run draws the flow snapshot it executes, not the flow being edited.
	const flow = $derived(app.runView?.flow ?? null);
	const snapshotKey = $derived(flow ? `${app.selectedRunId}:${flow.id}:${flow.version}` : '');
	const direction = $derived(flow?.settings?.direction ?? 'LR');

	$effect(() => {
		const _k = snapshotKey;
		const f = untrack(() => flow);
		const sagas = f ? cardSagas(f, untrack(() => app.shownState)) : new Map();
		const connected = new Map<string, string[]>();
		for (const e of f?.edges ?? []) connected.set(e.source, [...(connected.get(e.source) ?? []), e.sourceHandle || 'out']);
		nodes = [
			...(f?.nodes ?? []).map((n) => ({
				id: n.id,
				type: 'step' as const,
				position: { ...n.position },
				data: { node: n, run: true, direction, saga: sagas.get(n.id), connected: connected.get(n.id) },
				draggable: false,
				connectable: false,
				deletable: false,
				selected: n.id === untrack(() => app.selectedNodeId)
			})),
			...((f ? ghostNodes(f, untrack(() => app.shownState), direction) : []) as unknown as CardNode[])
		];
	});

	// Bands and boxes follow the cards' measured sizes, which grow as a run
	// reports progress (5 of 5 done, registered undos).
	$effect(() => {
		const measured = nodes.filter((n) => !isGhost(n.id));
		const f = untrack(() => flow);
		if (!f || !measured.some((n) => n.measured?.height)) return;
		const pos = new Map<string, Position>(measured.map((n) => [n.id, n.position]));
		const sizes = new Map(measured.map((n) => [n.id, { width: n.measured?.width ?? 248, height: n.measured?.height ?? 96 }]));
		bands = laneBands(f, pos, sizes);
		scopes = [...bodyScopes(f, pos, sizes), ...sagaScopes(f, pos, sizes)];
	});

	// Edges follow each poll: green where the run went, dashed where it did
	// not, animated into whatever is running now.
	let edges = $state.raw<Edge[]>([]);
	$effect(() => {
		edges = flow ? [...toEdges(flow, app.shownState, false), ...sagaEdges(flow, app.shownState, false)] : [];
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
			minZoom={0.2}
			maxZoom={1.8}
			nodesDraggable={false}
			nodesConnectable={false}
			deleteKey={null}
			onnodeclick={({ node }) => (app.selectedNodeId = isGhost(node.id) ? (node.data as unknown as { member: string }).member : node.id)}
			onpaneclick={() => (app.selectedNodeId = null)}
		>
			<LaneBands {bands} />
			<Scopes {scopes} status={sagaStatus} select={(id) => (app.selectedNodeId = id)} />
			<Background variant={BackgroundVariant.Dots} gap={22} size={1.4} />
			<Controls showLock={false} />
			<FitOnResize {width} fitKey={snapshotKey} />
		</SvelteFlow>
	</div>
</SvelteFlowProvider>
