<script lang="ts">
	import { onDestroy } from 'svelte';
	import { useSvelteFlow } from '@xyflow/svelte';

	// Lives inside <SvelteFlow>. Refits the view when the canvas width changes
	// (a side panel collapsed or opened) or when the drawn flow changes.
	let { width, fitKey }: { width: number; fitKey: string } = $props();

	const { fitView } = useSvelteFlow();

	let lastWidth = 0;
	let lastKey = '';
	let timer: ReturnType<typeof setTimeout> | undefined;

	$effect(() => {
		const w = width;
		const k = fitKey;
		if (!w || !k) return;
		if (k === lastKey && Math.abs(w - lastWidth) <= 24) return;
		lastKey = k;
		lastWidth = w;
		// Let nodes measure and panel transitions settle before fitting.
		clearTimeout(timer);
		timer = setTimeout(() => fitView({ padding: 0.08, maxZoom: 1.15, duration: 200 }), 160);
	});

	onDestroy(() => clearTimeout(timer));
</script>
