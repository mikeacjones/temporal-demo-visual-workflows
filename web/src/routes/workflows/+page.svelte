<script lang="ts">
	import { confirmAction } from '$lib/confirm.svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Plus from '@lucide/svelte/icons/plus';
	import Upload from '@lucide/svelte/icons/upload';
	import Search from '@lucide/svelte/icons/search';
	import Copy from '@lucide/svelte/icons/copy';
	import Download from '@lucide/svelte/icons/download';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import Ellipsis from '@lucide/svelte/icons/ellipsis';
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import Webhook from '@lucide/svelte/icons/webhook';
	import Hand from '@lucide/svelte/icons/hand';
	import { toast } from 'svelte-sonner';
	import * as Table from '$lib/components/ui/table';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { api } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import { shell } from '$lib/shell.svelte';
	import { ago, executionTone } from '$lib/status';
	import { downloadFlow, readFlowFile } from '$lib/flowio';
	import type { FlowSummary, RunSummary } from '$lib/types';

	let q = $state('');
	let lastRuns = $state<Record<string, RunSummary>>({});
	let fileInput: HTMLInputElement;

	async function loadRuns() {
		try {
			const runs = (await api.allRuns({ limit: 100 })) ?? [];
			const latest: Record<string, RunSummary> = {};
			for (const r of runs) if (r.flowId && !latest[r.flowId]) latest[r.flowId] = r;
			lastRuns = latest;
		} catch {
			/* column stays empty */
		}
	}
	onMount(() => {
		shell.loadFlows();
		loadRuns();
		const t = setInterval(loadRuns, 5000);
		return () => clearInterval(t);
	});

	const rows = $derived(
		shell.flows.filter((f) => !q || `${f.name} ${f.description ?? ''}`.toLowerCase().includes(q.toLowerCase()))
	);
	const triggerIcon = { schedule: CalendarClock, webhook: Webhook, manual: Hand } as const;

	async function duplicate(f: FlowSummary) {
		try {
			const { flow } = await api.getFlow(f.id);
			const { id: _i, version: _v, updatedAt: _u, ...rest } = flow;
			const { flow: copy } = await api.createFlow({ ...rest, name: `${flow.name} (copy)` });
			await shell.loadFlows();
			toast.success(`Duplicated as "${copy.name}"`);
		} catch (err) {
			toast.error(`Could not duplicate: ${errorMessage(err)}`);
		}
	}
	async function exportFlow(f: FlowSummary) {
		try {
			downloadFlow((await api.getFlow(f.id)).flow);
		} catch (err) {
			toast.error(`Could not export: ${errorMessage(err)}`);
		}
	}
	async function remove(f: FlowSummary) {
		const ok = await confirmAction({
			title: `Delete "${f.name}"?`,
			description: 'Its schedule is removed too. Runs already started keep going.',
			confirmLabel: 'Delete',
			destructive: true
		});
		if (!ok) return;
		try {
			await api.deleteFlow(f.id);
			if (app.flow?.id === f.id) app.closeFlow();
			await shell.loadFlows();
		} catch (err) {
			toast.error(`Could not delete: ${errorMessage(err)}`);
		}
	}
	async function toggleSchedule(f: FlowSummary, enabled: boolean) {
		try {
			await api.setSchedule(f.id, enabled);
			await shell.loadFlows();
			toast.success(enabled ? `Schedule on for "${f.name}"` : `Schedule off for "${f.name}"`);
		} catch (err) {
			toast.error(`Could not change the schedule: ${errorMessage(err)}`);
		}
	}
	async function importFile(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!file) return;
		try {
			const created = await api.createFlow(await readFlowFile(file));
			toast.success(`Imported "${created.flow.name}"`);
			await app.openCreated(created);
		} catch (err) {
			toast.error(`Import failed: ${errorMessage(err)}`);
		} finally {
			fileInput.value = '';
		}
	}
</script>

<svelte:head><title>Workflows · Visual Workflows</title></svelte:head>

<div class="h-full overflow-y-auto">
	<PageHeader eyebrow="Workflows" title="Your" accent="workflows" description="Each saved version is immutable; runs execute the version they started with.">
		{#snippet actions()}
			<input type="file" accept="application/json,.json" class="hidden" bind:this={fileInput} onchange={importFile} />
			<Button variant="outline" size="sm" class="mono" onclick={() => fileInput.click()}><Upload /> Import JSON</Button>
			<Button size="sm" class="btn-uv" onclick={() => app.newFlow()}><Plus /> New workflow</Button>
		{/snippet}
	</PageHeader>

	<div class="px-8 pb-8">
		<div class="mb-3 flex items-center gap-2">
			<div class="relative w-80">
				<Search class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-[var(--slate-300)]" />
				<Input class="pl-8" placeholder="Search workflows" bind:value={q} />
			</div>
			<span class="mono text-xs text-[var(--slate-300)]">{rows.length} of {shell.flows.length}</span>
		</div>

		{#if shell.flowsLoaded && !shell.flows.length}
			<div class="flex flex-col items-center gap-3 border border-dashed border-[var(--line)] py-12 text-center">
				<p class="text-sm text-[var(--slate-300)]">No workflows yet. Start from a template?</p>
				<div class="flex gap-2">
					<Button class="btn-uv" onclick={() => app.newFlow()}><Plus /> New workflow</Button>
					<Button variant="outline" class="mono" href="/templates">Browse templates</Button>
				</div>
			</div>
		{:else}
			<div class="border border-[var(--line)] bg-[#14161e]/85">
				<Table.Root>
					<Table.Header>
						<Table.Row class="hover:bg-transparent">
							<Table.Head class="eyebrow !text-[0.6rem]">Name</Table.Head>
							<Table.Head class="eyebrow !text-[0.6rem]">Trigger</Table.Head>
							<Table.Head class="eyebrow !text-[0.6rem]">Schedule</Table.Head>
							<Table.Head class="eyebrow !text-[0.6rem]">Last run</Table.Head>
							<Table.Head class="eyebrow !text-[0.6rem]">Version</Table.Head>
							<Table.Head class="eyebrow !text-[0.6rem]">Updated</Table.Head>
							<Table.Head class="w-10"></Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#each rows as f (f.id)}
							{@const Icon = triggerIcon[f.triggerKind as keyof typeof triggerIcon] ?? Hand}
							{@const last = lastRuns[f.id]}
							<Table.Row class="cursor-pointer" onclick={() => goto(`/workflows/${encodeURIComponent(f.id)}`)}>
								<Table.Cell class="max-w-[380px]">
									<div class="flex items-center gap-2">
										<span class="truncate font-semibold">{f.name}</span>
										{#if f.keyed}<span class="status-badge text-[var(--cyan)]" data-tip="One long-lived run per key">keyed</span>{/if}
									</div>
									{#if f.description}<div class="truncate text-xs text-[var(--slate-300)]">{f.description}</div>{/if}
								</Table.Cell>
								<Table.Cell><span class="mono flex items-center gap-1.5 text-xs text-[var(--slate-300)]"><Icon class="size-3.5" />{f.triggerKind}</span></Table.Cell>
								<Table.Cell onclick={(e) => e.stopPropagation()}>
									{#if f.triggerKind === 'schedule'}
										<Switch checked={f.scheduleActive} onCheckedChange={(v) => toggleSchedule(f, v)} />
									{:else}
										<span class="text-xs text-[var(--slate-500)]">—</span>
									{/if}
								</Table.Cell>
								<Table.Cell>
									{#if last}
										{@const tone = executionTone(last.status)}
										<a
											href="/executions/{encodeURIComponent(last.workflowId)}"
											onclick={(e) => e.stopPropagation()}
											class="mono text-xs"
											style:color={tone.text}>{tone.label} · {ago(last.startTime)}</a
										>
									{:else}<span class="text-xs text-[var(--slate-500)]">never</span>{/if}
								</Table.Cell>
								<Table.Cell class="mono text-xs">v{f.version}</Table.Cell>
								<Table.Cell class="mono text-xs text-[var(--slate-300)]">{ago(f.updatedAt)}</Table.Cell>
								<Table.Cell onclick={(e) => e.stopPropagation()}>
									<DropdownMenu.Root>
										<DropdownMenu.Trigger>
											{#snippet child({ props })}
												<Button {...props} variant="ghost" size="icon-sm" aria-label="More actions"><Ellipsis /></Button>
											{/snippet}
										</DropdownMenu.Trigger>
										<DropdownMenu.Content align="end">
											<DropdownMenu.Item onclick={() => duplicate(f)}><Copy /> Duplicate</DropdownMenu.Item>
											<DropdownMenu.Item onclick={() => exportFlow(f)}><Download /> Export JSON</DropdownMenu.Item>
											<DropdownMenu.Separator />
											<DropdownMenu.Item variant="destructive" onclick={() => remove(f)}><Trash2 /> Delete</DropdownMenu.Item>
										</DropdownMenu.Content>
									</DropdownMenu.Root>
								</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			</div>
		{/if}
	</div>
</div>
