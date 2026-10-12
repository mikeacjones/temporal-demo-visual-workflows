<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Plus from '@lucide/svelte/icons/plus';
	import LayoutTemplate from '@lucide/svelte/icons/layout-template';
	import ArrowRight from '@lucide/svelte/icons/arrow-right';
	import { Button } from '$lib/components/ui/button';
	import ZiggyDrift from '$lib/components/ZiggyDrift.svelte';
	import Starfield from '$lib/components/Starfield.svelte';
	import WorkerPill from '$lib/components/WorkerPill.svelte';
	import { api } from '$lib/api';
	import { app } from '$lib/state.svelte';
	import { shell } from '$lib/shell.svelte';
	import { PERSON_WAITS } from '$lib/progress';
	import { ago, executionTone } from '$lib/status';
	import type { RunSummary, ScheduleInfo, Waiting } from '$lib/types';

	// Legacy deep links (/?flow=…&mode=runs&run=…) now live under /workflows/<id>.
	const legacyFlow = page.url.searchParams.get('flow');
	if (legacyFlow) {
		const rest = new URLSearchParams(page.url.search);
		rest.delete('flow');
		const q = rest.toString();
		goto(`/workflows/${encodeURIComponent(legacyFlow)}${q ? `?${q}` : ''}`, { replaceState: true });
	}

	let runs = $state<RunSummary[]>([]);
	let schedules = $state<ScheduleInfo[]>([]);
	/** Running runs that are waiting on a person or an outside event, with what they wait for. */
	let waiting = $state<{ run: RunSummary; wait: Waiting }[]>([]);
	let loaded = $state(false);

	async function load() {
		try {
			const [r, s] = await Promise.all([api.allRuns({ limit: 100 }), api.schedules().catch(() => [])]);
			runs = r ?? [];
			schedules = s ?? [];
			// The run list has no progress, so look at running runs individually (a memo read, no worker).
			const running = runs.filter((x) => x.status === 'Running').slice(0, 25);
			const views = await Promise.all(running.map((x) => api.getRun(x.workflowId).catch(() => null)));
			waiting = views.flatMap((v, i) => {
				const w = Object.values(v?.progress?.nodes ?? {}).find((n) => n.s === 'waiting' && n.w && PERSON_WAITS.includes(n.w.kind))?.w;
				return w ? [{ run: running[i], wait: w }] : [];
			});
		} catch {
			/* the tiles show zeros */
		} finally {
			loaded = true;
		}
	}

	onMount(() => {
		load();
		const t = setInterval(load, 5000);
		return () => clearInterval(t);
	});

	const midnight = () => {
		const d = new Date();
		d.setHours(0, 0, 0, 0);
		return d.getTime();
	};
	const today = $derived(runs.filter((r) => new Date(r.startTime).getTime() >= midnight()));
	const byStatus = $derived(
		today.reduce<Record<string, number>>((m, r) => ((m[r.status] = (m[r.status] ?? 0) + 1), m), {})
	);
	const runningNow = $derived(runs.filter((r) => r.status === 'Running').length);
	const activeSchedules = $derived(schedules.filter((s) => !s.paused).length);
	const failed = $derived(runs.filter((r) => ['Failed', 'TimedOut', 'Terminated'].includes(r.status)).slice(0, 6));
	const attention = $derived([
		...waiting.filter((w) => w.wait.kind === 'approval').map((w) => ({ run: w.run, why: w.wait.title ? `Approval: ${w.wait.title}` : 'Waiting for approval', tone: 'var(--purple)' })),
		...failed.map((r) => ({ run: r, why: r.status === 'TimedOut' ? 'Timed out' : r.status, tone: 'var(--red)' }))
	]);

	const tiles = $derived([
		{ label: 'Runs today', value: today.length, sub: [['Completed', byStatus.Completed], ['Failed', byStatus.Failed], ['Running', byStatus.Running]].filter(([, n]) => n).map(([k, n]) => `${n} ${String(k).toLowerCase()}`).join(' · ') || '—' },
		{ label: 'Running now', value: runningNow, sub: 'executions in flight' },
		{ label: 'Waiting', value: waiting.length, sub: 'on people, signals, conditions', wait: true },
		{ label: 'Active schedules', value: activeSchedules, sub: `${schedules.length - activeSchedules} paused` }
	]);
	const openRun = (r: RunSummary) => goto(`/executions/${encodeURIComponent(r.workflowId)}`);
</script>

<svelte:head><title>Overview · Visual Workflows</title></svelte:head>

<div class="h-full overflow-y-auto">
	<section class="relative overflow-hidden border-b border-[var(--line)]">
		<Starfield />
		<ZiggyDrift />
		<div class="relative flex flex-wrap items-center gap-8 px-8 pt-8 pb-7">
			<div class="max-w-2xl min-w-0 flex-1">
				<div class="eyebrow">Visual workflow builder · Temporal</div>
				<h1 class="mt-2 text-[2.6rem] leading-[1.03] font-light tracking-[-0.04em]">
					Draw it. <span class="text-gradient-uv">Temporal runs it durably.</span>
				</h1>
				<p class="mt-3 max-w-xl text-sm text-[var(--slate-300)]">
					Every step is a real Temporal primitive: Activities with retry policies, Signals and Updates, durable timers, child Workflows. Runs survive
					crashes, deploys, and days of waiting.
				</p>
				<div class="mt-5 flex flex-wrap items-center gap-2">
					<Button class="btn-uv" onclick={() => app.newFlow()}><Plus /> New workflow</Button>
					<Button variant="outline" class="mono" href="/templates"><LayoutTemplate /> Browse templates</Button>
					<span class="ml-2"><WorkerPill /></span>
				</div>
			</div>
		</div>
	</section>

	<div class="space-y-6 px-8 py-6">
		<div class="grid grid-cols-2 gap-3 xl:grid-cols-5">
			{#each tiles as t (t.label)}
				<div class="border border-[var(--line)] bg-[#14161e]/85 p-4">
					<div class="eyebrow !text-[0.6rem]">{t.label}</div>
					<div class="mt-1 text-4xl font-light tabular-nums {t.wait && t.value ? 'text-[var(--purple)]' : t.value ? 'text-gradient-green' : ''}">
						{loaded ? t.value : '–'}
					</div>
					<div class="mono mt-1 text-[0.62rem] text-[var(--slate-300)]">{t.sub}</div>
				</div>
			{/each}
			<div class="border border-[var(--line)] bg-[#14161e]/85 p-4">
				<div class="eyebrow !text-[0.6rem]">Workers</div>
				<div class="mt-2 text-lg font-light">{shell.serverless ? 'Serverless' : 'Long-lived'}</div>
				<div class="mono mt-1 text-[0.62rem]" style:color={shell.pill.color}>{shell.pill.text}</div>
				<div class="mono mt-0.5 text-[0.6rem] text-[var(--slate-500)]">queue {shell.session?.taskQueue ?? '…'}</div>
			</div>
		</div>

		<div class="grid gap-6 xl:grid-cols-[1.4fr_1fr]">
			<section class="border border-[var(--line)] bg-[#14161e]/85">
				<div class="flex items-center justify-between border-b border-[var(--line)] px-4 py-2.5">
					<span class="eyebrow">Recent executions</span>
					<a href="/executions" class="mono flex items-center gap-1 text-xs">All executions <ArrowRight class="size-3" /></a>
				</div>
				<div class="divide-y divide-[var(--line)]">
					{#each runs.slice(0, 9) as r (r.workflowId)}
						{@const tone = executionTone(r.status)}
						{@const w = waiting.find((x) => x.run.workflowId === r.workflowId)}
						<button class="flex w-full items-center gap-3 px-4 py-2 text-left hover:bg-[var(--accent)]" onclick={() => openRun(r)}>
							{#if w}<span class="pill-wait !text-[0.55rem]">waiting</span>{:else if r.status === 'Running'}<span class="pill-live !text-[0.55rem]">running</span>{:else}<span class="status-badge" style:color={tone.text}>{tone.label}</span>{/if}
							<span class="min-w-0 flex-1 truncate text-sm">{r.flowName ?? shell.flowName(r.flowId)} <span class="mono text-[0.66rem] text-[var(--slate-300)]">v{r.flowVersion ?? '?'}</span></span>
							{#if r.key}<span class="mono text-[0.64rem] text-[var(--cyan)]">{r.key}</span>{/if}
							<span class="mono text-[0.64rem] text-[var(--slate-300)]">{r.source ?? ''}</span>
							<span class="mono w-16 text-right text-[0.64rem] text-[var(--slate-300)]">{ago(r.startTime)}</span>
						</button>
					{:else}
						<div class="px-4 py-8 text-center text-sm text-[var(--slate-300)]">{loaded ? 'No executions yet.' : 'Loading…'}</div>
					{/each}
				</div>
			</section>

			<section class="border border-[var(--line)] bg-[#14161e]/85">
				<div class="border-b border-[var(--line)] px-4 py-2.5"><span class="eyebrow">Needs attention</span></div>
				<div class="divide-y divide-[var(--line)]">
					{#each attention as a (a.run.workflowId + a.why)}
						<button class="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-[var(--accent)]" onclick={() => openRun(a.run)}>
							<span class="inline-block size-2 shrink-0" style:background={a.tone}></span>
							<span class="min-w-0 flex-1">
								<span class="block truncate text-sm">{a.run.flowName ?? shell.flowName(a.run.flowId)}</span>
								<span class="block truncate text-[0.68rem]" style:color={a.tone}>{a.why}</span>
							</span>
							<span class="mono text-[0.64rem] text-[var(--slate-300)]">{ago(a.run.startTime)}</span>
						</button>
					{:else}
						<p class="px-4 py-6 text-sm text-[var(--slate-300)]">Nothing needs you. Failed runs and pending approvals show up here.</p>
					{/each}
				</div>
			</section>
		</div>
	</div>
</div>
