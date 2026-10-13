<script lang="ts">
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import HeartPulse from '@lucide/svelte/icons/heart-pulse';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import JsonBlock from './JsonBlock.svelte';
	import ApprovalCard from './ApprovalCard.svelte';
	import FixCard from './FixCard.svelte';
	import EventWaitPanel from './EventWaitPanel.svelte';
	import UpdateCard from './UpdateCard.svelte';
	import AgentPanel from './AgentPanel.svelte';
	import VarsPanel from './VarsPanel.svelte';
	import LoadDetails from './LoadDetails.svelte';
	import { app } from '$lib/state.svelte';
	import { NODE_SPECS } from '$lib/nodes';
	import { ago, clock, duration, executionTone, nodeTone, pretty, waitingText } from '$lib/status';
	import type { OnUpdateConfig } from '$lib/types';

	const node = $derived(app.selectedNode);
	const rs = $derived(node ? app.nodeRunState(node.id) : undefined);
	const pending = $derived(node ? app.pendingActivity(node.id) : undefined);
	const tone = $derived(nodeTone(rs?.status, !!pending && pending.attempt > 1));
	const state = $derived(app.lastState);
	const exec = $derived(executionTone(app.runView?.execution.status));

	// Step outputs keyed the way expressions see them (only once details are loaded).
	const outputs = $derived.by(() => {
		if (!state || !app.details) return undefined;
		const out: Record<string, unknown> = {};
		for (const n of state.flow.nodes) {
			const s = state.nodes[n.id];
			if (s?.output !== undefined && s.status === 'completed') out[n.key] = s.output;
		}
		return out;
	});
	const eventNodes = ['waitForSignal', 'waitForCondition', 'onSignal'];
</script>

<ScrollArea class="h-full">
	<div class="space-y-4 p-4">
		{#if node}
			{@const spec = NODE_SPECS[node.type]}
			<div>
				<div class="eyebrow" style:color={spec.accent}>{spec.label} · {spec.primitive}</div>
				<div class="mt-1 text-lg font-semibold">{node.label}</div>
				<div class="mono text-[0.66rem] text-[var(--slate-300)]">steps.{node.key}</div>
			</div>

			<div class="grid grid-cols-3 gap-2 border border-[var(--line)] p-2.5">
				<div>
					<div class="eyebrow !text-[0.58rem]">Status</div>
					<div class="mono mt-0.5 text-sm font-semibold uppercase" style:color={tone.text}>{tone.label || 'pending'}</div>
				</div>
				<div>
					<div class="eyebrow !text-[0.58rem]">Started</div>
					<div class="mono mt-0.5 text-xs">{clock(rs?.startedAt)}</div>
				</div>
				<div>
					<div class="eyebrow !text-[0.58rem]">Took</div>
					<div class="mono mt-0.5 text-xs">{rs?.startedAt ? duration(rs.startedAt, rs.endedAt) : '—'}</div>
				</div>
			</div>

			{#if rs?.handle}
				<p class="text-xs text-[var(--slate-300)]">Took the <span class="mono text-[var(--green)]">{rs.handle}</span> outcome.</p>
			{/if}
			{#if rs?.progress}
				<p class="mono text-xs text-[var(--yellow)]">{rs.progress}</p>
			{/if}
			{#if rs?.undo}
				<p class="flex items-center gap-1.5 text-xs text-[var(--pink)]"><Undo2 class="size-3.5" /> Undo request {rs.undo}</p>
			{/if}
			{#if rs?.childWorkflowId}
				<a class="mono flex items-center gap-1 text-xs" href="/executions/{encodeURIComponent(rs.childWorkflowId)}">
					Open child run <ExternalLink class="size-3" />
				</a>
			{/if}

			{#if pending}
				<div class="space-y-1 border p-2.5 text-xs" style:border-color={pending.attempt > 1 ? 'var(--pink)' : 'var(--line)'}>
					<div class="eyebrow !text-[0.6rem]" style:color={pending.attempt > 1 ? 'var(--pink)' : undefined}>Activity {pending.activityType}</div>
					<div>Attempt <span class="mono font-semibold">{pending.attempt}</span>{pending.maxAttempts ? ` of ${pending.maxAttempts}` : ''} · {pending.state}</div>
					{#if pending.state === 'Started' && pending.lastHeartbeatTime}
						<div class="flex items-start gap-1.5 text-[var(--green)]" title="The attempt heartbeats while it waits. A missed heartbeat means its worker is gone, and Temporal retries within seconds; a cancellation reaches the attempt on its next heartbeat.">
							<HeartPulse class="mt-0.5 size-3.5 shrink-0" />
							<span class="mono break-all">heartbeat {ago(pending.lastHeartbeatTime)}{pending.heartbeat ? ` · ${pending.heartbeat.phase}` : ''}</span>
						</div>
					{/if}
					{#if pending.lastFailure}
						<div class="mono break-words text-[var(--slate-300)]">Last failure: {pending.lastFailure}</div>
						<div class="text-[var(--pink)]">
							Temporal will retry{pending.nextAttemptTime ? ` at ${clock(pending.nextAttemptTime)}` : ''}; the retry policy spaces the attempts, the
							Workflow code does not loop.
						</div>
					{/if}
				</div>
			{/if}

			{#if rs?.status === 'waiting' && rs.waiting}
				{#if rs.waiting.kind === 'approval'}
					<ApprovalCard nodeId={node.id} waiting={rs.waiting} />
				{:else if rs.waiting.kind === 'fix'}
					<FixCard nodeId={node.id} waiting={rs.waiting} />
				{:else if !['signal', 'condition'].includes(rs.waiting.kind)}
					<div class="border border-[var(--purple)] bg-[var(--purple)]/10 p-3 text-sm">{waitingText(rs.waiting)}</div>
				{/if}
			{/if}

			{#if eventNodes.includes(node.type)}
				<EventWaitPanel {node} {rs} />
			{/if}
			{#if node.type === 'onUpdate'}
				{@const u = node.config as OnUpdateConfig}
				{#if rs?.invocations}<p class="mono text-xs text-[var(--purple)]">{rs.invocations} updates accepted so far</p>{/if}
				<UpdateCard updateName={u.updateName} validator={u.validator} />
			{/if}

			{#if node.type === 'agent' && rs && rs.status !== 'pending' && rs.status !== 'skipped'}
				<AgentPanel nodeId={node.id} childWorkflowId={rs.childWorkflowId} />
			{/if}

			{#if rs?.error}
				<div class="space-y-1">
					<div class="eyebrow !text-[0.62rem] !text-[var(--red)]">Error</div>
					<pre class="json !text-[var(--red)]">{rs.error}</pre>
				</div>
			{/if}

			{#if rs && rs.status !== 'pending' && rs.status !== 'skipped'}
				{#if app.details}
					<JsonBlock value={rs.output} label="Output" />
				{:else}
					<LoadDetails />
				{/if}
			{/if}
		{:else if app.runView}
			<div>
				<div class="eyebrow">Run</div>
				<div class="mt-1 text-lg font-semibold" style:color={exec.text}>{exec.label}</div>
				<p class="mt-1 text-xs text-[var(--slate-300)]">Select a step to see its details and actions.</p>
			</div>
			{#if state}
				<div class="grid grid-cols-2 gap-2 border border-[var(--line)] p-2.5 text-xs">
					<div><div class="eyebrow !text-[0.58rem]">Source</div><div class="mono mt-0.5">{state.source || '—'}</div></div>
					<div><div class="eyebrow !text-[0.58rem]">Flow version</div><div class="mono mt-0.5">v{state.flowVersion}</div></div>
					<div><div class="eyebrow !text-[0.58rem]">Started</div><div class="mono mt-0.5">{clock(state.startedAt)}</div></div>
					<div><div class="eyebrow !text-[0.58rem]">Duration</div><div class="mono mt-0.5">{duration(state.startedAt, state.endedAt)}</div></div>
					{#if state.key}
						<div><div class="eyebrow !text-[0.58rem]">Key</div><div class="mono mt-0.5 text-[var(--green)]">{state.key}</div></div>
						<div>
							<div class="eyebrow !text-[0.58rem]">Generation</div>
							<div class="mono mt-0.5" title="Continue-As-New count">{state.generation ?? 0}</div>
						</div>
					{/if}
				</div>
				{#if state.error}<pre class="json !text-[var(--red)]">{state.error}</pre>{/if}
				<VarsPanel />
				{#if Object.keys(state.events ?? {}).length}
					<div class="space-y-1.5">
						<div class="eyebrow !text-[0.62rem]">Events received</div>
						{#each Object.entries(state.events).sort(([a], [b]) => a.localeCompare(b)) as [name, ev] (name)}
							<div class="border border-[var(--line)] p-2 text-xs">
								<div class="flex items-center justify-between">
									<span class="mono text-[var(--indigo-100)]">{name}</span>
									<span class="mono text-[0.6rem] text-[var(--slate-300)]">×{ev.count}{ev.lastAt ? ` · ${clock(ev.lastAt)}` : ''}</span>
								</div>
								{#if ev.last !== undefined}<pre class="json mt-1 !py-1 !text-[0.62rem]">{pretty(ev.last)}</pre>{/if}
							</div>
						{/each}
					</div>
				{/if}
				{#if app.runView.callbackUrl && app.runActive}
					<div class="space-y-1">
						<div class="eyebrow !text-[0.62rem]">Callback URL</div>
						<p class="mono text-[0.62rem] break-all text-[var(--slate-300)]">{app.runView.callbackUrl}/events/&lt;signal&gt;</p>
					</div>
				{/if}
				{#if app.details}
					<JsonBlock value={outputs} label="Step outputs" open={state.status !== 'running'} />
				{:else}
					<LoadDetails label="Load step outputs" />
				{/if}
			{/if}
			<div class="mono text-[0.62rem] text-[var(--slate-500)]">History events: {app.runView.execution.historyLength}</div>
		{/if}
	</div>
</ScrollArea>
