<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { SvelteFlow, Background, BackgroundVariant, Controls, useSvelteFlow, useUpdateNodeInternals, type Connection, type Edge, type NodeTypes } from '@xyflow/svelte';
	import SchemaNode, { type SchemaData, type SchemaNodeType } from './SchemaNode.svelte';

	// The mapper's canvas: the data a step can see on the left, its inputs on
	// the right, and a line per mapping. Dragging from a field to an input maps
	// it; deleting a line clears the input.
	let {
		source,
		target,
		edges: mappingEdges,
		connect,
		clear,
		selectEdge
	}: {
		source: SchemaData;
		target: SchemaData;
		edges: Edge[];
		connect: (sourcePath: string, targetPath: string) => void;
		clear: (targetPath: string) => void;
		selectEdge: (targetPath: string) => void;
	} = $props();

	const nodeTypes: NodeTypes = { schema: SchemaNode };
	const updateNodeInternals = useUpdateNodeInternals();
	const { fitView } = useSvelteFlow();

	let nodes = $state.raw<SchemaNodeType[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let fitted = false;

	// Rows come and go as fields expand, collapse, or get added: keep the
	// nodes where they are, swap their data, and re-measure their handles.
	$effect(() => {
		const src = source;
		const dst = target;
		const current = untrack(() => nodes);
		const at = (id: string, x: number) => current.find((n) => n.id === id)?.position ?? { x, y: 0 };
		nodes = [
			{ id: 'src', type: 'schema', position: at('src', 0), data: src, deletable: false },
			{ id: 'dst', type: 'schema', position: at('dst', 560), data: dst, deletable: false }
		];
		tick().then(() => {
			updateNodeInternals(['src', 'dst']);
			if (!fitted) {
				fitted = true;
				setTimeout(() => fitView({ padding: 0.08, maxZoom: 1.1 }), 60);
			}
		});
	});

	$effect(() => {
		const e = mappingEdges;
		tick().then(() => (edges = e));
	});

	function onconnect(c: Connection) {
		if (c.source === 'src' && c.target === 'dst' && c.sourceHandle && c.targetHandle) connect(c.sourceHandle, c.targetHandle);
		// The mapping, not the drag, decides what lines there are.
		edges = mappingEdges;
	}

	const isValidConnection = (c: Edge | Connection) => c.source === 'src' && c.target === 'dst';
</script>

<SvelteFlow
	bind:nodes
	bind:edges
	{nodeTypes}
	colorMode="dark"
	minZoom={0.3}
	maxZoom={1.6}
	deleteKey={['Delete', 'Backspace']}
	{onconnect}
	{isValidConnection}
	ondelete={({ edges: gone }) => gone.forEach((e) => e.targetHandle && clear(e.targetHandle))}
	onedgeclick={({ edge }) => edge.targetHandle && selectEdge(edge.targetHandle)}
	defaultEdgeOptions={{ selectable: true }}
>
	<Background variant={BackgroundVariant.Dots} gap={22} size={1.4} />
	<Controls showLock={false} />
</SvelteFlow>
