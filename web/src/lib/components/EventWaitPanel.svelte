<script lang="ts">
	import Copy from '@lucide/svelte/icons/copy';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import SignalCard from './SignalCard.svelte';
	import LoadDetails from './LoadDetails.svelte';
	import { app } from '$lib/state.svelte';
	import { ago, clock, pretty } from '$lib/status';
	import { eventUrl } from '$lib/progress';
	import type { FlowNode, NodeState, OnSignalConfig, WaitConditionConfig } from '$lib/types';

	// For Wait for signal, Wait for condition and On signal steps: what the step
	// waits for, what has arrived, and every way to send the next event.
	let { node, rs }: { node: FlowNode; rs?: NodeState } = $props();

	const cfg = $derived(node.config as Partial<WaitConditionConfig & OnSignalConfig>);
	const signalName = $derived(cfg.signalName ?? '');
	const event = $derived(signalName ? app.lastState?.events?.[signalName] : undefined);
	const callback = $derived(app.runView?.callbackUrl && signalName ? eventUrl(app.runView.callbackUrl, signalName) : '');
	const isWaiting = $derived(rs?.status === 'waiting');
	const lane = $derived(node.type === 'onSignal');

	function copy(text: string) {
		navigator.clipboard?.writeText(text);
		toast.success('Copied');
	}
</script>

<div class="space-y-3">
	<div class="space-y-1 border border-[var(--line)] p-2.5">
		<div class="eyebrow !text-[0.6rem]">{lane ? 'Handles' : 'Waits for'}</div>
		{#if node.type === 'waitForCondition'}
			<div class="mono text-[0.78rem] text-[var(--yellow)]">{cfg.condition}</div>
			<p class="text-[0.68rem] text-[var(--slate-300)]">Re-checked when an event arrives or a variable changes. No polling.</p>
		{:else if lane}
			<div class="mono text-[0.78rem] text-[var(--purple)]">Every "{signalName}" event</div>
			<p class="text-[0.68rem] text-[var(--slate-300)]">
				This lane runs once per event, in arrival order{rs?.invocations ? ` — ${rs.invocations} handled so far` : ''}.
			</p>
		{:else}
			<div class="mono text-[0.78rem] text-[var(--purple)]">Signal "{signalName}"</div>
			<p class="text-[0.68rem] text-[var(--slate-300)]">Consumes the next event, including one that arrived before this step started.</p>
		{/if}
	</div>

	{#if signalName}
		<div class="space-y-1.5 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="eyebrow !text-[0.6rem]">Latest "{signalName}" event</span>
				{#if event}
					<span class="mono text-[0.6rem] text-[var(--slate-300)]">
						{event.count} received{node.type === 'waitForSignal' && event.pending ? ` · ${event.pending} not yet consumed` : ''}
					</span>
				{/if}
			</div>
			{#if event && event.count > 0}
				<div class="mono text-[0.66rem] text-[var(--slate-300)]">at {clock(event.lastAt)} · {ago(event.lastAt)}</div>
				{#if event.last !== undefined}
					<pre class="json max-h-48 overflow-auto">{pretty(event.last)}</pre>
				{:else}
					<LoadDetails label="Load payload" />
				{/if}
			{:else if !app.lastState?.events || !Object.keys(app.lastState.events).length}
				<LoadDetails label="Load latest events" />
			{:else}
				<p class="text-[0.7rem] text-[var(--slate-300)]">Nothing yet. The run is idle until one arrives.</p>
			{/if}
		</div>
	{/if}

	<!-- Only useful while the run can still receive events. -->
	{#if callback && app.runActive}
		<div class="space-y-1.5">
			<div class="eyebrow !text-[0.6rem]">Callback URL for outside systems</div>
			<div class="flex gap-1.5">
				<Input class="mono text-[0.66rem]" readonly value={callback} />
				<Button variant="outline" size="icon-sm" onclick={() => copy(callback)} data-tip="Copy URL" aria-label="Copy URL"><Copy /></Button>
			</div>
			<pre class="json !text-[0.6rem]">curl -X POST {callback} -H 'Content-Type: application/json' -d '{'{"status":"shipped"}'}'</pre>
			<p class="text-[0.66rem] text-[var(--slate-300)]">
				or send Signal <span class="mono text-[var(--indigo-100)]">{signalName}</span> from the Temporal UI.
			</p>
		</div>
	{/if}

	{#if signalName && app.runActive}
		<SignalCard
			{signalName}
			until={rs?.waiting?.until}
			waiting={isWaiting}
			initialPayload={event?.last !== undefined ? pretty(event.last) : undefined}
		/>
	{/if}
</div>
