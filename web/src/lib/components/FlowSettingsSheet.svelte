<script lang="ts">
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';
	import ArrowRightFromLine from '@lucide/svelte/icons/arrow-right-from-line';
	import ArrowDownFromLine from '@lucide/svelte/icons/arrow-down-from-line';
	import * as Sheet from '$lib/components/ui/sheet';
	import * as ToggleGroup from '$lib/components/ui/toggle-group';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import ActivitySettings from './ActivitySettings.svelte';
	import { app } from '$lib/state.svelte';
	import { SYSTEM_ACTIVITY_DEFAULTS, type Direction, type IDConflictPolicy, type IDReusePolicy } from '$lib/types';

	const flow = $derived(app.flow);

	const conflictPolicies = [
		{ value: 'use_existing', label: 'Use the running run' },
		{ value: 'fail', label: 'Refuse the start' },
		{ value: 'terminate_existing', label: 'Terminate it and start afresh' }
	];
	const reusePolicies = [
		{ value: 'allow_duplicate', label: 'Always start a new run' },
		{ value: 'allow_duplicate_failed_only', label: "Only if the last run didn't succeed" },
		{ value: 'reject_duplicate', label: 'Never: one run per key' }
	];
	const conflictNotes: Record<IDConflictPolicy, string> = {
		use_existing: 'Starting the flow again for the key reaches its run (the start input is dropped). Messages sent with start are delivered to it.',
		fail: 'Starting the flow again for the key is an error while its run is running, and so is Update-With-Start. Signal-With-Start still delivers to the running run: that is what it is for.',
		terminate_existing: 'Starting the flow again, or sending a message with start, terminates the running run and starts a fresh one in its place.'
	};
	const reuseNotes: Record<IDReusePolicy, string> = {
		allow_duplicate: "Once the key's run has closed, the next start (or message with start) begins a fresh run.",
		allow_duplicate_failed_only: "Once the key's run has closed, a new one starts only if it failed, was cancelled, terminated or timed out. A key that completed successfully stays done.",
		reject_duplicate: "A key runs once. While the closed run is retained in the namespace, starting the key again is refused, and so is any message sent with start."
	};
	const validJson = (t?: string) => {
		if (!t?.trim()) return true;
		try {
			JSON.parse(t);
			return true;
		} catch {
			return false;
		}
	};
</script>

<Sheet.Root bind:open={app.settingsOpen}>
	<Sheet.Content side="right" class="w-[440px] !max-w-[440px] gap-0 p-0">
		<Sheet.Header class="border-b border-[var(--line)] p-4">
			<Sheet.Title>Flow settings</Sheet.Title>
			<Sheet.Description>Applies to every step and every run of this flow.</Sheet.Description>
		</Sheet.Header>
		{#if flow}
			<ScrollArea class="min-h-0 flex-1">
				<div class="space-y-5 p-4">
					<Field label="Name"><Input bind:value={flow.name} /></Field>
					<Field label="Description"><Textarea class="min-h-16 text-sm" bind:value={flow.description} /></Field>
					<Field label="Layout direction" hint="Main outcomes continue the line; errors and timeouts branch off to the side.">
						<ToggleGroup.Root
							type="single"
							value={flow.settings.direction ?? 'LR'}
							onValueChange={(v) => v && app.setDirection(v as Direction)}
							variant="outline"
							size="sm"
							class="w-full"
						>
							<ToggleGroup.Item value="LR" class="mono flex-1 gap-1.5 text-xs"><ArrowRightFromLine class="size-3.5" /> Left to right</ToggleGroup.Item>
							<ToggleGroup.Item value="TB" class="mono flex-1 gap-1.5 text-xs"><ArrowDownFromLine class="size-3.5" /> Top-down</ToggleGroup.Item>
						</ToggleGroup.Root>
					</Field>

					<div class="space-y-2">
						<div class="flex items-center justify-between">
							<span class="eyebrow !text-[0.62rem]">Variables</span>
							<Button variant="outline" size="xs" class="mono" onclick={() => flow.settings.variables!.push({ name: '', initial: '', visible: false })}><Plus /> variable</Button>
						</div>
						<p class="text-[0.68rem] text-[var(--slate-300)]">
							Run state that steps read as <span class="mono text-[var(--green)]">vars.name</span> and Set variable steps change. Visible variables can be read from outside the run (a Query) without adding to its history.
						</p>
						{#each flow.settings.variables ?? [] as v, i (i)}
							<div class="grid grid-cols-[1fr_1fr_auto_auto] items-center gap-1.5">
								<Input class="mono text-xs" bind:value={v.name} placeholder="name" />
								<Input class="mono text-xs" bind:value={v.initial} placeholder="initial JSON" aria-invalid={!validJson(v.initial)} />
								<label class="flex items-center gap-1 text-[0.62rem] text-[var(--slate-300)]" title="Readable from outside the run">
									<Switch size="sm" bind:checked={() => !!v.visible, (c) => (v.visible = c)} /> visible
								</label>
								<Button variant="ghost" size="icon-sm" onclick={() => flow.settings.variables!.splice(i, 1)}><X /></Button>
							</div>
						{/each}
					</div>

					<div class="space-y-2">
						<span class="eyebrow !text-[0.62rem]">Activity defaults</span>
						<p class="text-[0.68rem] text-[var(--slate-300)]">Every step that calls out inherits these unless it sets its own.</p>
						<ActivitySettings bind:options={flow.settings.activity} inherited={SYSTEM_ACTIVITY_DEFAULTS} inheritedFrom="system defaults" open />
					</div>

					<div class="space-y-2 border border-[var(--line)] p-3">
						<span class="eyebrow !text-[0.62rem]">Priority</span>
						<ToggleGroup.Root
							type="single"
							value={String(flow.settings.priority || 3)}
							onValueChange={(v) => v && (flow.settings.priority = v === '3' ? undefined : Number(v))}
							variant="outline"
							size="sm"
							class="w-full"
						>
							{#each ['1', '2', '3', '4', '5'] as p (p)}
								<ToggleGroup.Item value={p} class="mono flex-1 text-xs">{p}{p === '1' ? ' first' : p === '5' ? ' last' : ''}</ToggleGroup.Item>
							{/each}
						</ToggleGroup.Root>
						<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
							When the task queue is backed up, workers take this flow's tasks in priority order; its Activities and child Workflows inherit it. Runs are also
							spread fairly between users (a fairness key per user), so one user's burst can't starve the others.
						</p>
					</div>

					<div class="space-y-2 border border-[var(--line)] p-3">
						<span class="eyebrow !text-[0.62rem]">Lifetime</span>
						<label class="flex items-center justify-between gap-3 text-sm">
							Entity workflow
							<Switch size="sm" bind:checked={() => !!flow.settings.entity, (c) => (flow.settings.entity = c)} />
						</label>
						<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
							{#if flow.settings.entity}
								The run lives as long as the thing it models: after its main path, it keeps serving events until an End run step (for example On signal close_account →
								End run), or for ever if the flow has none. Waiting costs nothing and adds no history, so it can be quiet for months; the run continues as new when
								Temporal suggests it, carrying its state.
							{:else}
								The run completes when its main path does, like any Workflow. To keep it open for events for a while, end the main path with a Delay or a wait.
							{/if}
						</p>
					</div>

					<div class="space-y-2 border border-[var(--line)] p-3">
						<span class="eyebrow !text-[0.62rem]">Keyed runs</span>
						<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
							One run per key: by default, starting the flow (or sending a message with start) for a key that already has a run reaches that run instead of starting another, so senders
							address it by key: the key is part of its Workflow ID. Unkeyed runs get a Workflow ID of their own at start. Once a key's run has completed, its next event starts a fresh one.
						</p>
						<Field label="Keyed by (expression over the start input)">
							<Input class="mono text-xs" bind:value={() => flow.settings.keyedBy ?? '', (v) => (flow.settings.keyedBy = v || undefined)} placeholder="e.g. input.customerId — blank for one run per start" />
						</Field>
						{#if flow.settings.keyedBy}
							<Field label="If the key's run is running (ID conflict policy)">
								<Choice
									bind:value={() => flow.settings.idConflictPolicy ?? 'use_existing', (v) => (flow.settings.idConflictPolicy = v === 'use_existing' ? undefined : (v as IDConflictPolicy))}
									options={conflictPolicies}
								/>
							</Field>
							<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{conflictNotes[flow.settings.idConflictPolicy ?? 'use_existing']}</p>
							<Field label="If the key's run has closed (ID reuse policy)">
								<Choice
									bind:value={() => flow.settings.idReusePolicy ?? 'allow_duplicate', (v) => (flow.settings.idReusePolicy = v === 'allow_duplicate' ? undefined : (v as IDReusePolicy))}
									options={reusePolicies}
								/>
							</Field>
							<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{reuseNotes[flow.settings.idReusePolicy ?? 'allow_duplicate']}</p>
						{/if}
					</div>
				</div>
			</ScrollArea>
		{/if}
	</Sheet.Content>
</Sheet.Root>
