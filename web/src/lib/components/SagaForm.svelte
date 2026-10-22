<script lang="ts">
	import X from '@lucide/svelte/icons/x';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import SagaMemberRow from './SagaMemberRow.svelte';
	import { app } from '$lib/state.svelte';
	import { NODE_SPECS } from '$lib/nodes';
	import { joinSaga, mainPath, sagaRoles } from '$lib/saga';
	import type { FlowNode, SagaConfig } from '$lib/types';

	// A Saga step: its members, how each one's failure counts, what undoes
	// each, and the outcomes that run once it has unwound.
	let { node }: { node: FlowNode } = $props();

	const c = $derived(node.config as SagaConfig);
	const flow = $derived(app.flow!);
	const label = (id: string) => flow.nodes.find((n) => n.id === id)?.label ?? id;
	const candidates = $derived.by(() => {
		const main = mainPath(flow);
		const roles = sagaRoles(flow);
		return flow.nodes
			.filter((n) => main.has(n.id) && !roles.has(n.id) && !['trigger', 'end', 'respond'].includes(n.type))
			.map((n) => ({ value: n.id, label: n.label }));
	});

	function add(id: string) {
		const step = flow.nodes.find((n) => n.id === id);
		if (step) joinSaga(flow, node, step);
		app.structure++;
	}
	function remove(id: string) {
		c.members = (c.members ?? []).filter((m) => m.step !== id);
		app.structure++;
	}
</script>

{#snippet note(s: string)}
	<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{s}</p>
{/snippet}

{@render note("Steps on the main path whose work can be undone. A member's compensation registers when the member completes, like a defer in Go. When a member whose failure unwinds the saga fails, the registered compensations run, newest first; then the saga takes its compensated outcome, or failed if a compensation failed. With nothing connected there, the run fails.")}
{@render note('Link a step by dragging from the ◆ handle under its card to this step, or add it below. Steps outside the saga are forward only: their failures never undo anything.')}

<div class="space-y-2">
	<span class="eyebrow !text-[0.62rem]">Members</span>
	{#each c.members ?? [] as m (m.step)}
		{@const step = flow.nodes.find((n) => n.id === m.step)}
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center gap-1.5">
				{#if step}<span class="inline-block size-2" style:background={NODE_SPECS[step.type].accent}></span>{/if}
				<button class="truncate text-left text-sm font-semibold hover:underline" onclick={() => (app.selectedNodeId = m.step)}>{label(m.step)}</button>
				<button class="ml-auto text-[var(--slate-500)] hover:text-[var(--red)]" onclick={() => remove(m.step)} aria-label="Remove {label(m.step)} from the saga"><X class="size-3.5" /></button>
			</div>
			<SagaMemberRow member={m} />
		</div>
	{:else}
		<p class="text-[0.68rem] text-[var(--slate-500)]">No members yet.</p>
	{/each}
	{#if candidates.length}
		<Field label="Add a step"><Choice value="" options={candidates} onchange={add} placeholder="Choose a step on the main path" /></Field>
	{/if}
</div>

<div class="space-y-1 border border-[var(--line)] p-2.5">
	<span class="eyebrow !text-[0.62rem]">Outcomes</span>
	{@render note(`compensated: it unwound cleanly. failed: a compensation ran out of retries. Steps there read steps.${node.key}.error, .failedStep (the member's key), .compensated (how many ran) and .failures. Compensation steps read steps.${node.key}.error too.`)}
</div>
