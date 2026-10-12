<script lang="ts">
	import Send from '@lucide/svelte/icons/send';
	import Wrench from '@lucide/svelte/icons/wrench';
	import Brain from '@lucide/svelte/icons/brain';
	import CornerDownRight from '@lucide/svelte/icons/corner-down-right';
	import MessageCircleQuestion from '@lucide/svelte/icons/message-circle-question';
	import User from '@lucide/svelte/icons/user';
	import Flag from '@lucide/svelte/icons/flag';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import { api } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import { clock, pretty } from '$lib/status';
	import type { AgentState, TraceEntry } from '$lib/types';

	let { nodeId, childWorkflowId }: { nodeId: string; childWorkflowId?: string } = $props();

	let agent = $state<AgentState | null>(null);
	let error = $state('');
	let reply = $state('');
	let busy = $state(false);

	const done = $derived(agent?.status === 'done' || agent?.status === 'failed');

	async function load() {
		const wid = app.selectedRunId;
		if (!wid) return;
		try {
			agent = await api.agent(wid, nodeId);
			error = '';
		} catch (err) {
			error = errorMessage(err);
		}
	}

	$effect(() => {
		const _ = [nodeId, childWorkflowId, app.selectedRunId];
		agent = null;
		load();
		const t = setInterval(() => {
			if (!done) load();
		}, 1500);
		return () => clearInterval(t);
	});

	async function answer() {
		const wid = app.selectedRunId;
		if (!wid || !reply.trim()) return;
		busy = true;
		try {
			await api.answer(wid, nodeId, reply.trim());
			reply = '';
			toast.success('Answer sent to the agent');
			await load();
		} catch (err) {
			toast.error(errorMessage(err));
		} finally {
			busy = false;
		}
	}

	const meta: Record<string, { icon: typeof Brain; color: string; label: string }> = {
		reasoning: { icon: Brain, color: 'var(--indigo-200)', label: 'Thought' },
		tool_call: { icon: Wrench, color: 'var(--cyan)', label: 'Tool call' },
		tool_result: { icon: CornerDownRight, color: 'var(--slate-300)', label: 'Result' },
		question: { icon: MessageCircleQuestion, color: 'var(--purple)', label: 'Asks a human' },
		answer: { icon: User, color: 'var(--purple)', label: 'Human answer' },
		final: { icon: Flag, color: 'var(--green)', label: 'Final answer' }
	};
	const m = (e: TraceEntry) => meta[e.kind] ?? { icon: Brain, color: 'var(--slate-300)', label: e.kind };
</script>

<div class="space-y-3">
	<div class="flex items-center justify-between">
		<span class="eyebrow">Agent trace · ReAct</span>
		{#if agent}
			<span class="mono text-[0.62rem] text-[var(--slate-300)]">turn {agent.turn}/{agent.maxTurns} · {agent.status}</span>
		{/if}
	</div>
	{#if childWorkflowId}
		<p class="mono text-[0.62rem] break-all text-[var(--slate-500)]">child workflow {childWorkflowId}</p>
	{/if}

	{#if error && !agent}
		<p class="text-xs text-[var(--slate-300)]">Agent not started yet, or no worker can answer the Query right now.</p>
	{/if}

	{#if agent}
		<ol class="space-y-2">
			{#each agent.trace as e, i (i)}
				{@const mm = m(e)}
				{@const Icon = mm.icon}
				<li class="border-l-2 pl-2.5" style:border-color={e.isError ? 'var(--red)' : mm.color}>
					<div class="flex items-center gap-1.5 text-[0.62rem]" style:color={e.isError ? 'var(--red)' : mm.color}>
						<Icon class="size-3" />
						<span class="mono tracking-wide uppercase">{mm.label}{e.tool ? ` · ${e.tool}` : ''}</span>
						<span class="mono ml-auto text-[var(--slate-500)]">{clock(e.at)}</span>
					</div>
					{#if e.text}
						<p
							class="mt-0.5 leading-snug whitespace-pre-wrap {e.kind === 'tool_result'
								? 'mono line-clamp-6 text-[0.68rem] text-[var(--slate-300)]'
								: 'text-[0.78rem]'}"
						>
							{e.text}
						</p>
					{/if}
					{#if e.input !== undefined && e.input !== null}
						<pre class="json mt-1 !py-1 !text-[0.64rem]">{pretty(e.input)}</pre>
					{/if}
				</li>
			{:else}
				<li class="text-xs text-[var(--slate-300)]">Waiting for the first model response…</li>
			{/each}
		</ol>

		{#if agent.pendingQuestion}
			<div class="space-y-2 border border-[var(--purple)] bg-[var(--purple)]/10 p-3">
				<div class="eyebrow !text-[var(--purple)]">The agent is asking</div>
				<p class="text-sm">{agent.pendingQuestion}</p>
				<Textarea class="min-h-16 text-sm" bind:value={reply} placeholder="Your answer" />
				<Button class="btn-uv w-full" disabled={busy || !reply.trim()} onclick={answer}><Send /> Answer</Button>
				<p class="text-[0.64rem] text-[var(--slate-300)]">
					The child Workflow waits durably for this Update. No worker is busy while it waits.
				</p>
			</div>
		{/if}
	{/if}
</div>
