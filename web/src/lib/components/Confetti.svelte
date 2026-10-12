<script lang="ts">
	// A brief burst in UV, purple and green when a run completes. Skipped
	// entirely under prefers-reduced-motion.
	let { fire = 0 }: { fire?: number } = $props();

	const colors = ['#444ce7', '#b664ff', '#1ff1a5', '#c3ff62', '#a5a9f5'];
	let pieces = $state<{ id: number; x: number; y: number; r: number; c: string; d: number }[]>([]);
	let seq = 0;

	$effect(() => {
		if (!fire) return;
		if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
		pieces = Array.from({ length: 36 }, (_, i) => {
			const a = (i / 36) * Math.PI * 2 + Math.random() * 0.3;
			const dist = 80 + Math.random() * 120;
			return { id: ++seq, x: Math.cos(a) * dist, y: Math.sin(a) * dist - 40, r: Math.random() * 360, c: colors[i % colors.length], d: 700 + Math.random() * 500 };
		});
		const t = setTimeout(() => (pieces = []), 1400);
		return () => clearTimeout(t);
	});
</script>

<div class="pointer-events-none absolute inset-0 grid place-items-center overflow-hidden" aria-hidden="true">
	{#each pieces as p (p.id)}
		<span
			class="piece"
			style="--x: {p.x}px; --y: {p.y}px; --r: {p.r}deg; background: {p.c}; animation-duration: {p.d}ms"
		></span>
	{/each}
</div>

<style>
	.piece {
		position: absolute;
		width: 7px;
		height: 7px;
		opacity: 0;
		animation: burst ease-out forwards;
	}
	@keyframes burst {
		0% {
			transform: translate(0, 0) rotate(0);
			opacity: 1;
		}
		100% {
			transform: translate(var(--x), calc(var(--y) + 60px)) rotate(var(--r));
			opacity: 0;
		}
	}
</style>
