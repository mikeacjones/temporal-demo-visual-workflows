<script lang="ts">
	import { NODE_SPECS } from '$lib/nodes';
	import { HANDLES, type Definition } from '$lib/types';

	// A cheap SVG thumbnail of a flow for cards: boxes and lines, no canvas.
	let { flow, height = 110 }: { flow: Definition; height?: number } = $props();

	const W = 248;
	const H = 90;
	const box = $derived.by(() => {
		let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
		for (const n of flow.nodes) {
			minX = Math.min(minX, n.position.x);
			minY = Math.min(minY, n.position.y);
			maxX = Math.max(maxX, n.position.x + W);
			maxY = Math.max(maxY, n.position.y + H);
		}
		const pad = 40;
		return isFinite(minX) ? `${minX - pad} ${minY - pad} ${maxX - minX + pad * 2} ${maxY - minY + pad * 2}` : '0 0 100 100';
	});
	const byId = $derived(new Map(flow.nodes.map((n) => [n.id, n])));
	const lr = $derived((flow.settings?.direction ?? 'LR') === 'LR');
</script>

<svg viewBox={box} preserveAspectRatio="xMidYMid meet" style:height="{height}px" class="w-full" aria-hidden="true">
	{#each flow.edges as e (e.id)}
		{@const a = byId.get(e.source)}
		{@const b = byId.get(e.target)}
		{#if a && b}
			{@const secondary = HANDLES[a.type][0] !== (e.sourceHandle || 'out')}
			{@const x1 = lr ? a.position.x + W : a.position.x + W / 2}
			{@const y1 = lr ? a.position.y + H / 2 : a.position.y + H}
			{@const x2 = lr ? b.position.x : b.position.x + W / 2}
			{@const y2 = lr ? b.position.y + H / 2 : b.position.y}
			<path
				d={lr ? `M${x1},${y1} H${(x1 + x2) / 2} V${y2} H${x2}` : `M${x1},${y1} V${(y1 + y2) / 2} H${x2} V${y2}`}
				fill="none"
				stroke={secondary ? '#b46ab8' : '#6169ed'}
				stroke-width="6"
				stroke-dasharray={secondary ? '14 12' : undefined}
				opacity="0.8"
			/>
		{/if}
	{/each}
	{#each flow.nodes as n (n.id)}
		<rect x={n.position.x} y={n.position.y} width={W} height={H} fill="#15171f" stroke-width="5" style:stroke={NODE_SPECS[n.type]?.accent ?? '#465a78'} />
		<rect x={n.position.x + 14} y={n.position.y + 22} width="42" height="42" opacity="0.35" style:fill={NODE_SPECS[n.type]?.accent ?? '#465a78'} />
		<rect x={n.position.x + 72} y={n.position.y + 30} width="140" height="12" fill="#cacbf9" opacity="0.45" />
		<rect x={n.position.x + 72} y={n.position.y + 52} width="100" height="9" fill="#92a4c3" opacity="0.3" />
	{/each}
</svg>
