<script lang="ts">
	import { ViewportPortal } from '@xyflow/svelte';
	import type { Band } from '$lib/layout';

	// Faint bands behind each event lane, drawn behind nodes and edges.
	let { bands }: { bands: Band[] } = $props();
</script>

<ViewportPortal target="back">
	{#each bands as b (b.id)}
		{@const tint = b.kind === 'saga' || b.kind === 'compensation' ? 'var(--pink)' : 'var(--purple)'}
		<div
			class="pointer-events-none absolute border border-dashed"
			style="transform: translate({b.x}px, {b.y}px); width: {b.width}px; height: {b.height}px;
				border-color: color-mix(in srgb, {tint} 28%, transparent);
				background: color-mix(in srgb, {tint} 5%, transparent);"
		>
			<span class="mono absolute top-1.5 left-2.5 text-[0.62rem] tracking-wider uppercase" style:color="color-mix(in srgb, {tint} 75%, var(--slate-300))">
				{b.label}
			</span>
		</div>
	{/each}
</ViewportPortal>
