<script lang="ts">
	import { ViewportPortal } from '@xyflow/svelte';
	import type { Scope } from '$lib/layout';

	// A box around each saga: its members, its Saga step and its compensations.
	// In a run it turns pink while unwinding, then green or red.
	let { scopes, status = {}, select }: { scopes: Scope[]; status?: Record<string, string | undefined>; select?: (id: string) => void } = $props();
	const tint = (id: string) =>
		status[id] === 'failed' ? 'var(--red)' : status[id] === 'compensated' ? 'var(--green)' : 'var(--pink)';
</script>

<ViewportPortal target="back">
	{#each scopes as s (s.id)}
		<div
			class="pointer-events-none absolute border-2"
			style="transform: translate({s.x}px, {s.y}px); width: {s.width}px; height: {s.height}px;
				border-color: color-mix(in srgb, {tint(s.id)} {status[s.id] ? 70 : 35}%, transparent);
				background: color-mix(in srgb, {tint(s.id)} {status[s.id] === 'unwinding' ? 9 : 4}%, transparent);"
		>
			<button
				class="mono pointer-events-auto absolute top-2 left-3 text-[0.64rem] tracking-wider uppercase hover:underline"
				style:color="color-mix(in srgb, {tint(s.id)} 85%, var(--slate-300))"
				onclick={() => select?.(s.id)}>◈ {s.label}{status[s.id] === 'unwinding' ? ' · unwinding' : status[s.id] === 'compensated' ? ' · unwound cleanly' : status[s.id] === 'failed' ? ' · a compensation failed' : ''}</button
			>
		</div>
	{/each}
</ViewportPortal>
