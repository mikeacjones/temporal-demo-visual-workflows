<script lang="ts">
	import type { Snippet } from 'svelte';
	import WorkerPill from './WorkerPill.svelte';

	let { eyebrow, title, accent, description, actions }: { eyebrow: string; title: string; accent?: string; description?: string; actions?: Snippet } = $props();
</script>

<header class="flex flex-wrap items-end gap-4 px-8 pt-7 pb-5">
	<div class="min-w-0">
		<div class="eyebrow">{eyebrow}</div>
		<h1 class="mt-1.5 text-[2rem] leading-[1.05] font-light tracking-[-0.03em]">
			{title}{#if accent}{' '}<span class="text-gradient-uv">{accent}</span>{/if}
		</h1>
		{#if description}<p class="mt-2 max-w-2xl text-sm text-[var(--slate-300)]">{description}</p>{/if}
	</div>
	<div class="ml-auto flex items-center gap-2">
		{@render actions?.()}
		<WorkerPill />
	</div>
</header>
