<script lang="ts">
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import { pretty } from '$lib/status';

	let { value, label = 'Output', open = true }: { value: unknown; label?: string; open?: boolean } = $props();
	const text = $derived(pretty(value));
</script>

{#if value !== undefined && value !== null && text !== ''}
	<Collapsible.Root {open}>
		<Collapsible.Trigger class="group eyebrow mb-1.5 flex cursor-pointer items-center gap-1 select-none !text-[0.62rem]">
			<ChevronRight class="size-3 transition-transform group-data-[state=open]:rotate-90" />
			{label}
		</Collapsible.Trigger>
		<Collapsible.Content>
			<pre class="json max-h-80 overflow-auto">{text}</pre>
		</Collapsible.Content>
	</Collapsible.Root>
{/if}
