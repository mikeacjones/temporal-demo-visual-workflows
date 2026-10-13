<script lang="ts">
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import Ban from '@lucide/svelte/icons/ban';
	import ShieldCheck from '@lucide/svelte/icons/shield-check';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import Moon from '@lucide/svelte/icons/moon';
	import Clock from '@lucide/svelte/icons/clock';
	import Reply from '@lucide/svelte/icons/reply';
	import { Button } from '$lib/components/ui/button';
	import RunCanvas from './RunCanvas.svelte';
	import Confetti from './Confetti.svelte';
	import { app } from '$lib/state.svelte';
	import { shell } from '$lib/shell.svelte';
	import { executionTone, clock } from '$lib/status';
	import { PERSON_WAITS } from '$lib/progress';

	// The run view: header, honest banners, and the canvas.
	let { showFlowName = false }: { showFlowName?: boolean } = $props();

	// Poll the open run every second while it is running.
	$effect(() => {
		const wid = app.selectedRunId;
		if (!wid) return;
		const t = setInterval(() => {
			if (!app.runView || app.runView.execution.status === 'Running') app.refreshRun();
		}, 1000);
		return () => clearInterval(t);
	});

	const exec = $derived(executionTone(app.runView?.execution.status));
	const run = $derived(app.lastState);
	const waits = $derived(
		Object.values(run?.nodes ?? {})
			.filter((n) => n.status === 'waiting' && n.waiting)
			.map((n) => n.waiting!)
	);
	const personWait = $derived(waits.find((w) => PERSON_WAITS.includes(w.kind)));
	const anyRunningStep = $derived(Object.values(run?.nodes ?? {}).some((n) => n.status === 'running'));
	// Waiting with nothing in flight: on serverless, no worker needs to run.
	const holding = $derived(app.runActive && !!personWait && !anyRunningStep && !app.runView?.pendingActivities?.length);
	// Celebrate when we watch a run finish, not when opening one that already has.
	let celebrate = $state(0);
	let lastStatus = '';
	let lastWid = '';
	$effect(() => {
		const status = app.runView?.execution.status ?? '';
		const wid = app.selectedRunId ?? '';
		if (wid === lastWid && lastStatus === 'Running' && status === 'Completed') celebrate++;
		lastStatus = status;
		lastWid = wid;
	});
</script>

<div class="flex h-full flex-col">
	{#if app.runView}
		<div class="flex shrink-0 items-center gap-3 border-b border-[var(--line)] bg-[#111218]/80 px-4 py-2">
			{#if app.runActive && personWait}
				<span class="pill-wait">{personWait.kind === 'approval' || personWait.kind === 'fix' ? 'Waiting on a person' : 'Waiting on an event'}</span>
			{:else if app.runActive}
				<span class="pill-live">Running</span>
			{:else}
				<span class="status-badge !py-0.5" style:color={exec.text}>{exec.label}</span>
			{/if}
			{#if showFlowName && run}
				<a href="/workflows/{encodeURIComponent(run.flowId)}" class="shrink-0 text-sm font-semibold text-[var(--off-white)] hover:underline">{run.flowName}</a>
			{/if}
			<span class="mono min-w-0 flex-1 truncate text-xs text-[var(--indigo-100)]" title={app.runView.workflowId}>{app.runView.workflowId}</span>
			{#if run}
				<span class="mono shrink-0 text-[0.66rem] whitespace-nowrap text-[var(--slate-300)]">
					{run.source ? `${run.source} · ` : ''}v{run.flowVersion}{run.key ? ` · ${run.flow.settings?.idleTimeoutSeconds ? 'session' : 'entity'} ${run.key}` : ''}{run.generation
						? ` · gen ${run.generation}`
						: ''} · started {clock(run.startedAt)}
				</span>
			{/if}
			<div class="ml-auto flex shrink-0 items-center gap-2 whitespace-nowrap">
				<a href={app.runView.temporalUrl} target="_blank" rel="noreferrer" class="mono flex items-center gap-1 text-xs">
					Open in Temporal UI <ExternalLink class="size-3" />
				</a>
				{#if app.runActive}
					<Button variant="outline" size="sm" class="mono" onclick={() => app.cancelRun()}><Ban /> Cancel</Button>
				{/if}
			</div>
		</div>
		{#if app.runView.execution.executionTime && app.runActive && !app.runView.progress}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--yellow)]/40 bg-[var(--yellow)]/10 px-4 py-2 text-sm">
				<Clock class="size-4 text-[var(--yellow)]" />
				Delayed start: this run exists (its ID and callback URL work, and Signals are kept) but begins at {clock(app.runView.execution.executionTime)}.
			</div>
		{/if}
		{#if app.earlyResponse?.workflowId === app.selectedRunId}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--purple)]/40 bg-[var(--purple)]/10 px-4 py-2 text-sm">
				<Reply class="size-4 shrink-0 text-[var(--purple)]" />
				{#if app.earlyResponse.error}
					<span>The run didn't respond: {app.earlyResponse.error}</span>
				{:else}
					<span class="shrink-0">Responded early, while the run carries on:</span>
					<code class="mono truncate text-xs text-[var(--green)]">{JSON.stringify(app.earlyResponse.response)}</code>
				{/if}
			</div>
		{/if}
		{#if run?.compensation === 'running'}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--pink)]/40 bg-[var(--pink)]/10 px-4 py-2 text-sm">
				<Undo2 class="size-4 text-[var(--pink)]" /> Compensating… a later step failed, so completed steps are being undone in reverse order.
			</div>
		{:else if run?.compensation === 'done' || run?.compensation === 'failed'}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--line)] px-4 py-2 text-sm text-[var(--slate-300)]">
				<Undo2 class="size-4" /> Compensation {run.compensation === 'done' ? 'finished: completed steps were undone.' : 'failed for at least one step; see the undo badges.'}
			</div>
		{/if}
		{#if app.runActive && shell.pill.unavailable && !shell.serverless}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--pink)]/40 bg-[var(--pink)]/10 px-4 py-2 text-sm">
				<ShieldCheck class="size-4 text-[var(--pink)]" />
				No worker is polling. Temporal has preserved this run's progress; it resumes when a worker returns.
			</div>
		{:else if app.runActive && shell.serverless && !shell.worker?.available && holding}
			<div class="flex shrink-0 items-center gap-2 border-b border-[var(--line)] px-4 py-2 text-sm text-[var(--indigo-100)]">
				<Moon class="size-4" /> Workers are scaled to zero. This run costs no compute while it waits; a worker starts when an event arrives.
			</div>
		{/if}
	{/if}

	<div class="relative min-h-0 flex-1">
		{#if app.runView?.flow}
			{#key app.selectedRunId}
				<RunCanvas />
			{/key}
			<Confetti fire={celebrate} />
		{:else if app.selectedRunId}
			<div class="grid h-full place-items-center text-sm text-[var(--slate-300)]">
				{app.runLoadError ? `Could not load the run: ${app.runLoadError}` : 'Loading run…'}
			</div>
		{:else}
			<div class="grid h-full place-items-center text-center">
				<div class="max-w-sm space-y-2">
					<p class="text-sm text-[var(--slate-300)]">No run selected. Pick a run, or start one from Build.</p>
				</div>
			</div>
		{/if}
	</div>
</div>
