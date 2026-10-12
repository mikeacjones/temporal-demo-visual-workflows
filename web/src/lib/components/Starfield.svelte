<script lang="ts">
	import { onMount } from 'svelte';

	// The buildkit's starfield and glow blobs, for hero areas only (never behind the canvas).
	let { blobs = true }: { blobs?: boolean } = $props();
	let canvas: HTMLCanvasElement;

	onMount(() => {
		const start = () => (window as unknown as { Starfield?: { start: (c: HTMLCanvasElement) => void } }).Starfield?.start(canvas);
		if ((window as unknown as { Starfield?: unknown }).Starfield) return start();
		const s = document.createElement('script');
		s.src = '/starfield.js';
		s.onload = start;
		document.head.appendChild(s);
	});
</script>

<div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
	<canvas bind:this={canvas} class="absolute inset-0 h-full w-full"></canvas>
	{#if blobs}
		<div class="blob pink"></div>
		<div class="blob green"></div>
	{/if}
</div>

<style>
	.blob {
		position: absolute;
		filter: blur(140px);
		border-radius: 100%;
		opacity: 0.32;
		animation: drift 9s ease-in-out infinite alternate;
	}
	.pink {
		background: linear-gradient(263deg, #db35d0cc 18%, #b664ffa3 49%, #b664ff29 70%);
		width: 900px;
		height: 420px;
		top: -260px;
		right: -200px;
	}
	.green {
		background: linear-gradient(92deg, #444ce780 27%, #1ff1a580 84%);
		width: 900px;
		height: 380px;
		bottom: -280px;
		left: -360px;
		animation-delay: 2s;
	}
	@keyframes drift {
		to {
			translate: 0 60px;
		}
	}
</style>
