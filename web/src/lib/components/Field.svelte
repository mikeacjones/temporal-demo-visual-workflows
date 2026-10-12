<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Label } from '$lib/components/ui/label';

	let { label, hint, children }: { label: string; hint?: string; children: Snippet } = $props();
</script>

<div class="space-y-1.5">
	<Label class="eyebrow !text-[0.62rem]">{label}</Label>
	{@render children()}
	{#if hint}
		<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{hint}</p>
	{/if}
</div>
