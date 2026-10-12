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
	import DurationInput from './DurationInput.svelte';
	import ActivitySettings from './ActivitySettings.svelte';
	import { app } from '$lib/state.svelte';
	import { SYSTEM_ACTIVITY_DEFAULTS, type Direction } from '$lib/types';

	const flow = $derived(app.flow);
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
						<span class="eyebrow !text-[0.62rem]">Keyed runs</span>
						<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
							One long-lived run per key: starting the flow (or sending an event) for a key that already has a run reaches that run instead of starting another.
							Continue-As-New keeps its history small and moves it onto new worker builds.
						</p>
						<Field label="Keyed by (expression over the start input)">
							<Input class="mono text-xs" bind:value={() => flow.settings.keyedBy ?? '', (v) => (flow.settings.keyedBy = v || undefined)} placeholder="e.g. input.customerId — blank for one run per start" />
						</Field>
						{#if flow.settings.keyedBy}
							<Field label="End the run after no events for" hint="Blank uses the default of 24 hours.">
								<DurationInput bind:value={flow.settings.idleTimeoutSeconds} placeholder="24 hours" />
							</Field>
						{/if}
					</div>
				</div>
			</ScrollArea>
		{/if}
	</Sheet.Content>
</Sheet.Root>
