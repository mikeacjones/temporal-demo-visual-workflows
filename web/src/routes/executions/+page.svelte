<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import * as Table from '$lib/components/ui/table';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Choice from '$lib/components/Choice.svelte';
	import { api } from '$lib/api';
	import { shell } from '$lib/shell.svelte';
	import { ago, clock, duration, executionTone } from '$lib/status';
	import type { RunSummary } from '$lib/types';

	const STATUSES = ['All', 'Running', 'Completed', 'Failed', 'Canceled', 'Terminated', 'TimedOut'];
	let status = $state(page.url.searchParams.get('status') ?? 'All');
	let flowId = $state(page.url.searchParams.get('flow') ?? '');
	let runs = $state<RunSummary[]>([]);
	let loaded = $state(false);

	async function load() {
		try {
			runs = (await api.allRuns({ status: status === 'All' ? undefined : status, flowId: flowId || undefined, limit: 200 })) ?? [];
		} catch {
			/* keep the last list */
		} finally {
			loaded = true;
		}
	}

	// Reload on filter change, and poll every 3s.
	$effect(() => {
		const _f = [status, flowId];
		load();
		const t = setInterval(load, 3000);
		return () => clearInterval(t);
	});
	onMount(() => shell.loadFlows());

	const flowOptions = $derived([{ value: '', label: 'All flows' }, ...shell.flows.map((f) => ({ value: f.id, label: f.name }))]);
</script>

<svelte:head><title>Executions · Visual Workflows</title></svelte:head>

<div class="h-full overflow-y-auto">
	<PageHeader eyebrow="Executions" title="Every run," accent="across every flow" description="Each row is a Temporal Workflow Execution. Click one to see it step by step." />

	<div class="px-8 pb-8">
		<div class="mb-3 flex flex-wrap items-center gap-2">
			{#each STATUSES as s (s)}
				{@const tone = executionTone(s === 'All' ? undefined : s)}
				<button
					class="mono border px-2.5 py-1 text-[0.68rem] tracking-wide uppercase transition-colors"
					style:border-color={status === s ? 'var(--uv-light)' : 'var(--line)'}
					style:background={status === s ? 'rgba(68,76,231,.18)' : undefined}
					style:color={s === 'All' ? 'var(--off-white)' : tone.text}
					onclick={() => (status = s)}>{s === 'TimedOut' ? 'Timed out' : s}</button
				>
			{/each}
			<div class="ml-auto w-64"><Choice bind:value={flowId} options={flowOptions} placeholder="All flows" /></div>
		</div>

		<div class="border border-[var(--line)] bg-[#14161e]/85">
			<Table.Root>
				<Table.Header>
					<Table.Row class="hover:bg-transparent">
						<Table.Head class="eyebrow !text-[0.6rem]">Status</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Flow</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Source</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Key</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Started</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Duration</Table.Head>
						<Table.Head class="eyebrow !text-[0.6rem]">Workflow ID</Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#each runs as r (r.workflowId)}
						{@const tone = executionTone(r.status)}
						<Table.Row class="cursor-pointer" onclick={() => goto(`/executions/${encodeURIComponent(r.workflowId)}`)}>
							<Table.Cell>
								{#if r.status === 'Running'}<span class="pill-live !text-[0.55rem]">Running</span>{:else}<span class="status-badge" style:color={tone.text}>{tone.label}</span>{/if}
							</Table.Cell>
							<Table.Cell class="font-semibold">{r.flowName ?? shell.flowName(r.flowId)} <span class="mono text-[0.66rem] font-normal text-[var(--slate-300)]">v{r.flowVersion ?? '?'}</span></Table.Cell>
							<Table.Cell class="mono text-xs text-[var(--slate-300)]">{r.source ?? '—'}</Table.Cell>
							<Table.Cell class="mono text-xs text-[var(--cyan)]">{r.key ?? ''}</Table.Cell>
							<Table.Cell class="mono text-xs" data-tip={new Date(r.startTime).toLocaleString()}>{clock(r.startTime)} <span class="text-[var(--slate-300)]">· {ago(r.startTime)}</span></Table.Cell>
							<Table.Cell class="mono text-xs">{duration(r.startTime, r.closeTime)}</Table.Cell>
							<Table.Cell class="mono max-w-[260px] truncate text-[0.66rem] text-[var(--slate-300)]" data-tip={r.workflowId}>{r.workflowId}</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
			{#if loaded && !runs.length}
				<div class="flex flex-col items-center gap-2 py-10">
					<p class="text-sm text-[var(--slate-300)]">{status === 'All' ? 'No executions yet. Run a workflow to see it here.' : `No ${status.toLowerCase()} executions.`}</p>
				</div>
			{/if}
		</div>
		<p class="mono mt-2 text-[0.62rem] text-[var(--slate-500)]">Refreshes every 3 seconds from Temporal visibility.</p>
	</div>
</div>
