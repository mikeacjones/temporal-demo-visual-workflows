<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import * as ToggleGroup from '$lib/components/ui/toggle-group';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import DurationInput from './DurationInput.svelte';
	import { OVERLAP_OPTIONS, TIMEZONES, WEEKDAYS, scheduleSummary, scheduleType, type ScheduleType } from '$lib/schedule';
	import { UNITS } from '$lib/duration';
	import type { TriggerConfig } from '$lib/types';

	let { trigger = $bindable() }: { trigger: TriggerConfig } = $props();

	// Fold the original everySeconds field into the schedule config on first edit.
	if (!trigger.schedule) {
		trigger.schedule = trigger.everySeconds ? { every: trigger.everySeconds, everyUnit: 'seconds' } : { every: 5, everyUnit: 'minutes' };
		delete trigger.everySeconds;
	}
	const s = $derived(trigger.schedule!);
	let type = $state<ScheduleType>(scheduleType(trigger.schedule));

	function setType(t: string) {
		if (!t) return;
		type = t as ScheduleType;
		const keep = { timezone: s.timezone, overlap: s.overlap, catchupWindowSeconds: s.catchupWindowSeconds, jitterSeconds: s.jitterSeconds };
		if (type === 'interval') trigger.schedule = { ...keep, every: 5, everyUnit: 'minutes' };
		if (type === 'cron') trigger.schedule = { ...keep, cron: '0 9 * * 1-5' };
		if (type === 'calendar') trigger.schedule = { ...keep, calendar: { days: ['mon', 'tue', 'wed', 'thu', 'fri'], hour: 9, minute: 0 } };
	}
	const time = $derived(
		s.calendar ? `${String(s.calendar.hour).padStart(2, '0')}:${String(s.calendar.minute).padStart(2, '0')}` : '09:00'
	);
	function setTime(v: string) {
		const [h, m] = v.split(':').map(Number);
		if (s.calendar && isFinite(h) && isFinite(m)) {
			s.calendar.hour = h;
			s.calendar.minute = m;
		}
	}
</script>

<div class="space-y-3 border border-[var(--line)] p-2.5">
	<div class="flex items-center justify-between">
		<span class="eyebrow !text-[0.6rem]">Temporal Schedule</span>
		<span class="mono text-[0.66rem] text-[var(--green)]">{scheduleSummary(trigger)}</span>
	</div>
	<ToggleGroup.Root type="single" value={type} onValueChange={setType} variant="outline" size="sm" class="w-full">
		<ToggleGroup.Item value="interval" class="mono flex-1 text-xs">Interval</ToggleGroup.Item>
		<ToggleGroup.Item value="cron" class="mono flex-1 text-xs">Cron</ToggleGroup.Item>
		<ToggleGroup.Item value="calendar" class="mono flex-1 text-xs">Calendar</ToggleGroup.Item>
	</ToggleGroup.Root>

	{#if type === 'interval'}
		<Field label="Every">
			<div class="grid grid-cols-[1fr_110px] gap-1.5">
				<Input type="number" min="1" bind:value={s.every} />
				<Choice bind:value={s.everyUnit} options={UNITS.map((u) => ({ value: u.value, label: u.label }))} />
			</div>
		</Field>
	{:else if type === 'cron'}
		<Field label="Cron expression" hint="minute hour day-of-month month day-of-week, e.g. 0 9 * * 1-5">
			<Input class="mono" bind:value={s.cron} />
		</Field>
	{:else if s.calendar}
		<Field label="Days" hint="None selected means every day.">
			<ToggleGroup.Root type="multiple" bind:value={s.calendar.days} variant="outline" size="sm" class="w-full">
				{#each WEEKDAYS as d (d)}
					<ToggleGroup.Item value={d} class="mono flex-1 text-[0.65rem] uppercase">{d.slice(0, 2)}</ToggleGroup.Item>
				{/each}
			</ToggleGroup.Root>
		</Field>
		<Field label="At"><Input type="time" value={time} onchange={(e) => setTime(e.currentTarget.value)} /></Field>
	{/if}
	{#if type !== 'interval'}
		<Field label="Time zone"><Choice bind:value={s.timezone} options={TIMEZONES.map((z) => ({ value: z, label: z }))} placeholder="UTC" /></Field>
	{/if}
	<Field label="If the last run is still going">
		<Choice bind:value={s.overlap} options={OVERLAP_OPTIONS} placeholder="Skip if still running" />
	</Field>
	<Field label="Catch up missed runs within" hint="If the Schedule was unavailable, fire missed times from this window."><DurationInput bind:value={s.catchupWindowSeconds} placeholder="default" /></Field>
	<Field label="Jitter" hint="Spread fire times by up to this much."><DurationInput bind:value={s.jitterSeconds} placeholder="none" /></Field>
</div>
