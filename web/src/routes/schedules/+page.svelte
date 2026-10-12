<script lang="ts">
	import { onMount } from 'svelte';
	import Pause from '@lucide/svelte/icons/pause';
	import Play from '@lucide/svelte/icons/play';
	import Zap from '@lucide/svelte/icons/zap';
	import ArrowUpRight from '@lucide/svelte/icons/arrow-up-right';
	import { toast } from 'svelte-sonner';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { api } from '$lib/api';
	import { errorMessage } from '$lib/state.svelte';
	import { ago, clock } from '$lib/status';
	import type { ScheduleInfo } from '$lib/types';

	let schedules = $state<ScheduleInfo[]>([]);
	let loaded = $state(false);
	let noteFor = $state<ScheduleInfo | null>(null);
	let note = $state('');

	async function load() {
		try {
			schedules = (await api.schedules()) ?? [];
		} catch (err) {
			toast.error(`Could not load schedules: ${errorMessage(err)}`);
		} finally {
			loaded = true;
		}
	}
	onMount(() => {
		load();
		const t = setInterval(load, 10000);
		return () => clearInterval(t);
	});

	async function act(s: ScheduleInfo, action: 'pause' | 'unpause' | 'trigger', withNote?: string) {
		try {
			await api.scheduleAction(s.flowId, action, withNote);
			toast.success(action === 'trigger' ? `Started a run of "${s.flowName}" now` : action === 'pause' ? 'Schedule paused' : 'Schedule resumed');
			await load();
		} catch (err) {
			toast.error(errorMessage(err));
		}
	}

	function askPause(s: ScheduleInfo) {
		noteFor = s;
		note = '';
	}
	async function confirmPause() {
		if (!noteFor) return;
		const s = noteFor;
		noteFor = null;
		await act(s, 'pause', note || undefined);
	}
	const day = (iso: string) => new Date(iso).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
</script>

<svelte:head><title>Schedules · Visual Workflows</title></svelte:head>

<div class="h-full overflow-y-auto">
	<PageHeader
		eyebrow="Schedules"
		title="Temporal"
		accent="Schedules"
		description="Each scheduled flow is a Temporal Schedule: it starts a new Workflow at each fire time, handles overlap and catch-up, and can be paused with a note."
	/>
	<div class="grid gap-4 px-8 pb-8 xl:grid-cols-2">
		{#each schedules as s (s.flowId)}
			<section class="border bg-[#14161e]/85" style:border-color={s.paused ? 'var(--line)' : 'color-mix(in srgb, var(--green) 35%, var(--line))'}>
				<div class="flex items-start gap-3 border-b border-[var(--line)] px-4 py-3">
					<div class="min-w-0 flex-1">
						<a href="/workflows/{encodeURIComponent(s.flowId)}" class="flex items-center gap-1 font-semibold text-[var(--off-white)] hover:underline">
							{s.flowName}<ArrowUpRight class="size-3.5 text-[var(--slate-300)]" />
						</a>
						<div class="mono mt-0.5 text-xs text-[var(--green)]">{s.summary}</div>
					</div>
					{#if s.paused}<span class="status-badge text-[var(--slate-300)]">paused</span>{:else}<span class="pill-live !text-[0.55rem]">active</span>{/if}
				</div>
				{#if s.paused && s.note}
					<div class="border-b border-[var(--line)] px-4 py-2 text-xs text-[var(--indigo-100)]">Note: {s.note}</div>
				{/if}
				<div class="grid grid-cols-2 gap-4 px-4 py-3">
					<div>
						<div class="eyebrow !text-[0.58rem]">Next fire times</div>
						{#each s.nextRunTimes.slice(0, 3) as t (t)}
							<div class="mono mt-1 text-xs">{day(t)} · {clock(t)}</div>
						{:else}
							<div class="mt-1 text-xs text-[var(--slate-300)]">{s.paused ? 'None while paused' : '—'}</div>
						{/each}
					</div>
					<div>
						<div class="eyebrow !text-[0.58rem]">Recent runs</div>
						{#each s.recentRuns.slice(0, 3) as r (r.workflowId)}
							<a class="mono mt-1 block truncate text-xs" href="/executions/{encodeURIComponent(r.workflowId)}">{ago(r.scheduledAt)} · {r.workflowId.split('-').slice(-1)[0]}</a>
						{:else}
							<div class="mt-1 text-xs text-[var(--slate-300)]">None yet</div>
						{/each}
					</div>
				</div>
				<div class="flex items-center gap-2 border-t border-[var(--line)] px-4 py-2.5">
					{#if s.paused}
						<Button size="sm" variant="outline" class="mono" onclick={() => act(s, 'unpause')}><Play /> Resume</Button>
					{:else}
						<Button size="sm" variant="outline" class="mono" onclick={() => askPause(s)}><Pause /> Pause</Button>
					{/if}
					<Button size="sm" variant="outline" class="mono" onclick={() => act(s, 'trigger')}><Zap /> Run now</Button>
					{#if s.remainingActions !== undefined && s.remainingActions > 0}
						<span class="mono ml-auto text-[0.62rem] text-[var(--slate-300)]">{s.remainingActions} runs left in this demo</span>
					{/if}
				</div>
			</section>
		{:else}
			{#if loaded}
				<div class="col-span-full flex flex-col items-center gap-3 border border-dashed border-[var(--line)] py-12 text-center">
					<p class="text-sm text-[var(--slate-300)]">No schedules yet. Give a trigger the Schedule kind and switch it on.</p>
				</div>
			{/if}
		{/each}
	</div>
</div>

<Dialog.Root open={!!noteFor} onOpenChange={(o) => !o && (noteFor = null)}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Pause "{noteFor?.flowName}"?</Dialog.Title>
			<Dialog.Description>The Schedule keeps its configuration; no runs start until you resume. A note helps whoever finds it paused.</Dialog.Description>
		</Dialog.Header>
		<Input bind:value={note} placeholder="Why it's paused (optional)" />
		<Dialog.Footer>
			<Button variant="ghost" onclick={() => (noteFor = null)}>Cancel</Button>
			<Button class="btn-uv" onclick={confirmPause}><Pause /> Pause schedule</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
