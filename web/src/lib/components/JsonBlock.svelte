<script lang="ts">
	import { pretty } from '$lib/status';

	let { value, label = 'Output', open = true }: { value: unknown; label?: string; open?: boolean } = $props();
	const text = $derived(pretty(value));
</script>

{#if value !== undefined && value !== null && text !== ''}
	<details class="group" {open}>
		<summary class="eyebrow mb-1.5 cursor-pointer list-none select-none !text-[0.62rem]">
			<span class="inline-block transition-transform group-open:rotate-90">▸</span>
			{label}
		</summary>
		<pre class="json max-h-80 overflow-auto">{text}</pre>
	</details>
{/if}
