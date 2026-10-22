<script lang="ts">
	import { toast } from 'svelte-sonner';
	import Self from './OutputTree.svelte';
	import { typeLabel } from '$lib/connectors';
	import type { ConnectorField } from '$lib/types';

	// What a step returns, as a tree. Clicking a field copies the template
	// later steps use to read it.
	let { fields, base, accent }: { fields: ConnectorField[]; base: string; accent: string } = $props();

	function copy(path: string) {
		navigator.clipboard?.writeText(`{{ ${path} }}`);
		toast.success(`Copied {{ ${path} }}`);
	}
</script>

<ul class="space-y-0.5">
	{#each fields as f (f.name)}
		{@const path = `${base}.${f.name}`}
		{@const list = f.type === 'array'}
		<li>
			<button class="flex w-full items-center gap-1.5 text-left hover:text-[var(--off-white)]" onclick={() => copy(path)} data-tip={f.description ?? `Copy {{ ${path} }}`}>
				<span class="mono text-[0.68rem]" style:color={accent}>{f.name}{list ? '[]' : ''}</span>
				<span class="mono ml-auto text-[0.58rem] text-[var(--slate-500)]">{typeLabel(f)}</span>
			</button>
			{#if f.fields?.length}
				<div class="ml-2 border-l border-[var(--line)] pl-2">
					<Self fields={f.fields} base={list ? `${path}[0]` : path} {accent} />
				</div>
			{:else if list && f.open}
				<p class="ml-2 text-[0.62rem] text-[var(--slate-500)]">Fields as selected</p>
			{/if}
		</li>
	{/each}
</ul>
