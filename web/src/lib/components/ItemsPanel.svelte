<script lang="ts">
	import LoadDetails from './LoadDetails.svelte';
	import { app } from '$lib/state.svelte';
	import { nodeTone, pretty } from '$lib/status';

	// The items of a For each that runs steps. Picking one shows its own step
	// states on the body; "All" shows the summary across items.
	let { eachId }: { eachId: string } = $props();

	const items = $derived(app.lastState?.items?.[eachId] ?? []);
	const picked = $derived(app.selectedItem?.each === eachId ? app.selectedItem.index : null);
	const item = $derived(items.find((i) => i.index === picked));

	function pick(index: number | null) {
		app.selectedItem = index === null ? null : { each: eachId, index };
	}
</script>

<div class="space-y-1.5">
	<div class="eyebrow !text-[0.62rem]">Items</div>
	{#if !app.details}
		<LoadDetails label="Load items" />
	{:else if items.length === 0}
		<p class="text-xs text-[var(--slate-300)]">No items have started yet.</p>
	{:else}
		<div class="flex flex-wrap gap-1">
			<button
				class="mono border px-1.5 py-0.5 text-[0.62rem]"
				style:border-color={picked === null ? 'var(--indigo-100)' : 'var(--line)'}
				onclick={() => pick(null)}>All</button
			>
			{#each items as it (it.index)}
				{@const tone = nodeTone(it.status)}
				<button
					class="mono border px-1.5 py-0.5 text-[0.62rem]"
					style:color={tone.text}
					style:border-color={picked === it.index ? 'var(--indigo-100)' : tone.border}
					data-tip={it.error ?? tone.label}
					onclick={() => pick(it.index)}>#{it.index}</button
				>
			{/each}
		</div>
		<p class="text-[0.62rem] leading-snug text-[var(--slate-300)]">
			{picked === null ? 'The body shows every item together. Pick one to see its own steps.' : 'The body shows this item’s steps.'}
			The most recent items are kept, with failures kept longest.
		</p>
		{#if item}
			{#if item.item !== undefined}<pre class="json !py-1 !text-[0.62rem]">{pretty(item.item)}</pre>{/if}
			{#if item.error}<pre class="json !text-[var(--red)]">{item.error}</pre>{/if}
		{/if}
	{/if}
</div>
