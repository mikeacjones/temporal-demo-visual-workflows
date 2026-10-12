<script lang="ts">
	// Ziggies drifting through the banner like distant crew on slow orbits:
	// different sizes, paths, headings, and a gentle tumble. Smaller ones are
	// farther away, so they move slower and sit dimmer. Decorative only.
	//
	// ziggy.png has a black background (see the buildkit's brand guide), so each
	// moving wrapper blends with `lighten` and the page shows through the black.
	let { count = 7 }: { count?: number } = $props();

	type Drifter = {
		size: number;
		x0: number; y0: number; x1: number; y1: number;
		r0: number; r1: number;
		flip: number;
		duration: number;
		delay: number;
		opacity: number;
		blur: number;
	};

	const rand = (min: number, max: number) => min + Math.random() * (max - min);
	const sizes = [24, 32, 42, 54, 68, 86, 104, 120];

	function drifter(i: number): Drifter {
		const size = sizes[(i * 3 + Math.floor(rand(0, sizes.length))) % sizes.length];
		const depth = (size - 24) / (120 - 24); // 0 far, 1 near
		const rightward = Math.random() < 0.55;
		// Enter off one side, leave off the other, crossing the banner on a slant.
		const y0 = rand(-25, 105);
		const y1 = y0 + rand(-45, 45);
		return {
			size,
			x0: rightward ? -20 : 120,
			x1: rightward ? 120 : -20,
			y0,
			y1,
			r0: rand(-25, 25),
			r1: rand(-25, 25) + (Math.random() < 0.3 ? rand(-180, 180) : 0),
			// ziggy.png faces left, so mirror the ones heading right. Most face
			// where they're going; a few drift backwards.
			flip: (rightward ? -1 : 1) * (Math.random() < 0.2 ? -1 : 1),
			duration: 150 - depth * 85 + rand(-12, 12),
			delay: -rand(0, 150),
			// Kept below full strength so they never compete with the banner text.
			opacity: 0.3 + depth * 0.45,
			blur: depth < 0.2 ? 1 : 0
		};
	}

	const drifters = $derived(Array.from({ length: count }, (_, i) => drifter(i)));
</script>

<div class="drift" aria-hidden="true">
	{#each drifters as d, i (i)}
		<div
			class="drifter"
			style:--x0={d.x0}
			style:--y0={d.y0}
			style:--x1={d.x1}
			style:--y1={d.y1}
			style:--r0="{d.r0}deg"
			style:--r1="{d.r1}deg"
			style:--flip={d.flip}
			style:animation-duration="{d.duration}s"
			style:animation-delay="{d.delay}s"
			style:opacity={d.opacity}
			style:filter={d.blur ? `blur(${d.blur}px)` : undefined}
		>
			<img src="/ziggy.png" alt="" style:height="{d.size}px" />
		</div>
	{/each}
</div>

<style>
	.drift {
		position: absolute;
		inset: 0;
		overflow: hidden;
		pointer-events: none;
		container-type: size;
	}
	.drifter {
		position: absolute;
		top: 0;
		left: 0;
		mix-blend-mode: lighten;
		animation-name: cross;
		animation-timing-function: linear;
		animation-iteration-count: infinite;
		will-change: transform;
	}
	.drifter img {
		display: block;
		width: auto;
		max-width: none;
		transform: scaleX(var(--flip));
	}
	@keyframes cross {
		from {
			transform: translate(calc(var(--x0) * 1cqw), calc(var(--y0) * 1cqh)) translate(-50%, -50%) rotate(var(--r0));
		}
		to {
			transform: translate(calc(var(--x1) * 1cqw), calc(var(--y1) * 1cqh)) translate(-50%, -50%) rotate(var(--r1));
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.drifter {
			animation-play-state: paused;
		}
	}
</style>
