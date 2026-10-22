<script lang="ts">
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import Zap from '@lucide/svelte/icons/zap';
	import * as ToggleGroup from '$lib/components/ui/toggle-group';
	import { Button } from '$lib/components/ui/button';
	import Choice from './Choice.svelte';
	import { app } from '$lib/state.svelte';
	import { builtInRollback, compensationCandidates, customizeRollback, setCompensation } from '$lib/saga';
	import type { SagaMember } from '$lib/types';

	// One member's part in a saga: does its failure unwind the saga, and what
	// undoes it. Used by the Saga step's form and by the member's own.
	let { member }: { member: SagaMember } = $props();

	const flow = $derived(app.flow!);
	const step = $derived(flow.nodes.find((n) => n.id === member.step));
	const rollback = $derived(step ? builtInRollback(step) : undefined);
	const options = $derived([
		{ value: '', label: 'Nothing to undo' },
		...(rollback ? [{ value: '$builtin', label: `Built in: ${rollback.comp.note}` }] : []),
		...compensationCandidates(flow, member).map((n) => ({ value: n.id, label: `Step: ${n.label}` }))
	]);
	const touch = () => app.structure++;

	function setUndo(v: string) {
		if (v === '$builtin') {
			setCompensation(member, undefined);
			member.builtIn = true;
		} else {
			member.builtIn = undefined;
			setCompensation(member, v || undefined);
		}
		touch();
	}
	function customize() {
		const node = customizeRollback(flow, member);
		if (node) {
			touch();
			app.selectedNodeId = node.id;
		}
	}
</script>

<div class="space-y-2">
	<ToggleGroup.Root
		type="single"
		value={member.unwinds ? 'unwinds' : 'handled'}
		onValueChange={(v) => v && ((member.unwinds = v === 'unwinds'), touch())}
		variant="outline"
		size="sm"
		class="w-full"
	>
		<ToggleGroup.Item value="unwinds" class="mono flex-1 text-[0.66rem]"><Zap class="size-3" /> Failure unwinds</ToggleGroup.Item>
		<ToggleGroup.Item value="handled" class="mono flex-1 text-[0.66rem]">– Failure handled</ToggleGroup.Item>
	</ToggleGroup.Root>
	<div class="flex items-center gap-1.5">
		<Undo2 class="size-3.5 shrink-0 text-[var(--pink)]" />
		<div class="min-w-0 flex-1">
			<Choice value={member.builtIn ? '$builtin' : (member.compensation ?? '')} {options} onchange={setUndo} />
		</div>
	</div>
	{#if member.builtIn && rollback?.undo}
		<Button variant="outline" size="xs" class="mono" onclick={customize}>Customize: make it a {rollback.undo.label} step</Button>
	{/if}
	{#if member.compensation}
		<Button variant="ghost" size="xs" class="mono" onclick={() => (app.selectedNodeId = member.compensation!)}>Open the compensation step</Button>
	{/if}
</div>
