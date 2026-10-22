<script lang="ts">
	import { ViewportPortal } from '@xyflow/svelte';
	import type { Scope } from '$lib/layout';

	// A box around each saga (its members, its Saga step and its
	// compensations), and around each Loop's and For each's body. In a run a
	// saga's box turns pink while unwinding, then green or red.
	let { scopes, status = {}, select }: { scopes: Scope[]; status?: Record<string, string | undefined>; select?: (id: string) => void } = $props();
	const tint = (s: Scope) =>
		status[s.id] === 'failed' ? 'var(--red)' : status[s.id] === 'compensated' ? 'var(--green)' : s.color;
	const mark = (s: Scope) => (s.kind === 'saga' ? '◈' : '↻');
</script>

<ViewportPortal target="back">
	{#each scopes as s (s.id)}
		<div
			class="pointer-events-none absolute border-2"
			style="transform: translate({s.x}px, {s.y}px); width: {s.width}px; height: {s.height}px;
				border-color: color-mix(in srgb, {tint(s)} {status[s.id] ? 70 : 35}%, transparent);
				background: color-mix(in srgb, {tint(s)} {status[s.id] === 'unwinding' ? 9 : 4}%, transparent);"
		>
			<button
				class="mono pointer-events-auto absolute top-2 left-3 max-w-[calc(100%-1.5rem)] truncate text-left text-[0.64rem] tracking-wider whitespace-nowrap uppercase hover:underline"
				data-tip={s.label}
				style:color="color-mix(in srgb, {tint(s)} 85%, var(--slate-300))"
				onclick={() => select?.(s.id)}>{mark(s)} {s.label}{status[s.id] === 'unwinding' ? ' · unwinding' : status[s.id] === 'compensated' ? ' · unwound cleanly' : status[s.id] === 'failed' ? ' · a compensation failed' : ''}</button
			>
		</div>
	{/each}
</ViewportPortal>
