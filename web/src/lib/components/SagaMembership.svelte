<script lang="ts">
	import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { Button } from '$lib/components/ui/button';
	import Choice from './Choice.svelte';
	import SagaMemberRow from './SagaMemberRow.svelte';
	import { app } from '$lib/state.svelte';
	import { compensationSteps, joinSaga, leaveSaga, mainPath, sagaRoles } from '$lib/saga';
	import type { FlowNode } from '$lib/types';

	// A step's part in a saga, on its own form: what it compensates, or its
	// membership, or a way to join one.
	let { node }: { node: FlowNode } = $props();

	const flow = $derived(app.flow!);
	const role = $derived(sagaRoles(flow).get(node.id));
	const compensates = $derived(compensationSteps(flow).get(node.id));
	const sagas = $derived(flow.nodes.filter((n) => n.type === 'saga'));
	const onMain = $derived(mainPath(flow).has(node.id));
	const label = (id?: string) => flow.nodes.find((n) => n.id === id)?.label ?? id;
	const key = (id?: string) => flow.nodes.find((n) => n.id === id)?.key ?? id;
</script>

{#if compensates}
	{@const owner = sagaRoles(flow).get(compensates)?.saga}
	<div class="space-y-1.5 border border-[var(--pink)]/50 p-2.5">
		<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem] !text-[var(--pink)]"><Undo2 class="size-3" /> Compensation</span>
		<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
			Undoes <b>{label(compensates)}</b>. It runs only if {owner?.label ?? 'its saga'} unwinds after {label(compensates)} completed, newest first with the
			saga's other compensations. It reads that step's output as {'{{'} steps.{key(compensates)}.… {'}}'} and why the saga unwound as {'{{'} steps.{owner?.key}.error {'}}'}. Steps it leads to run as part of it.
		</p>
	</div>
{:else if role}
	<div class="space-y-2 border border-[var(--pink)]/50 p-2.5">
		<div class="flex items-center justify-between">
			<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem] !text-[var(--pink)]"><RotateCcw class="size-3" /> In {role.saga.label}</span>
			<button class="mono text-[0.62rem] text-[var(--slate-500)] hover:text-[var(--red)]" onclick={() => (leaveSaga(flow, node.id), app.structure++)}>Leave</button>
		</div>
		<SagaMemberRow member={role.member} />
		<p class="text-[0.66rem] leading-snug text-[var(--slate-500)]">
			{role.member.unwinds
				? 'If this step fails, the saga unwinds: what its members registered is undone, newest first.'
				: "If this step fails, nothing is undone: it takes its error outcome, or the run carries on past it."}
			{role.member.compensation || role.member.builtIn ? ' Once it completes, its undo is registered.' : ''}
		</p>
	</div>
{:else if sagas.length && onMain && !['trigger', 'end', 'respond'].includes(node.type)}
	<div class="flex items-center gap-2 border border-dashed border-[var(--line)] p-2.5">
		<RotateCcw class="size-3.5 shrink-0 text-[var(--pink)]" />
		<div class="min-w-0 flex-1">
			<Choice
				value=""
				options={sagas.map((s) => ({ value: s.id, label: `Join ${s.label}` }))}
				onchange={(id) => {
					const saga = sagas.find((s) => s.id === id);
					if (saga) joinSaga(flow, saga, node);
					app.structure++;
				}}
				placeholder="Join a saga"
			/>
		</div>
	</div>
{/if}
