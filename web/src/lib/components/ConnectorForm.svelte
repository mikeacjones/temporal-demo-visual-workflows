<script lang="ts">
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import ActivitySettings from './ActivitySettings.svelte';
	import MappingField from './MappingField.svelte';
	import OutputTree from './OutputTree.svelte';
	import DataMapper from './mapper/DataMapper.svelte';
	import { Button } from '$lib/components/ui/button';
	import Workflow from '@lucide/svelte/icons/waypoints';
	import { connectorSpec, missingInputs, outputFields, settleSettings } from '$lib/connectors';
	import type { ActivityOptions, ConnectorConfig, ConnectorField, ConnectorType, FlowNode } from '$lib/types';

	// A business connector step: its operation's settings, how its inputs are
	// mapped from the run's data, and what it returns. All of it is drawn from
	// the connector's spec (internal/connectors).
	let {
		node,
		inherited,
		sharedLimits
	}: {
		node: FlowNode;
		inherited: ActivityOptions;
		sharedLimits: { name: string; count: number; per: string }[];
	} = $props();

	const c = $derived(node.config as ConnectorConfig);
	const spec = $derived(connectorSpec(node.type as ConnectorType));
	const op = $derived(spec?.operations.find((o) => o.name === c.operation));
	const missing = $derived(spec && op ? missingInputs(spec, op, c) : []);
	const outputs = $derived(spec && op ? outputFields(spec, op, c) : []);
	const accent = $derived(`var(--${node.type})`);
	let mapperOpen = $state(false);

	// A step saved before settings existed keeps them in its mapping.
	$effect(() => {
		if (op) settleSettings(op, c);
	});

	const settingText = (f: ConnectorField) => {
		const v = c.settings?.[f.name];
		return typeof v === 'string' ? v : '';
	};
	const setSetting = (f: ConnectorField, v: string | boolean) => {
		c.settings ??= {};
		if (v === '' || v === false) delete c.settings[f.name];
		else c.settings[f.name] = v;
	};
	const patternError = (f: ConnectorField) => {
		const v = settingText(f);
		return f.pattern && v && !v.includes('{{') && !new RegExp(f.pattern).test(v) ? `Use ${f.patternHint}` : '';
	};
</script>

{#snippet note(s: string)}
	<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{s}</p>
{/snippet}

{#if !spec}
	{@render note('Loading the connector…')}
{:else}
	<div class="flex gap-2 border border-[var(--line)] p-2.5">
		<KeyRound class="mt-0.5 size-3.5 shrink-0" style="color: {accent}" />
		{@render note(`${spec.connection} No credential is stored in the flow, the Activity's input, or the run's history.`)}
	</div>
	<div class="border-l-2 py-0.5 pl-2.5" style:border-color={accent}>
		<div class="text-sm font-semibold">{spec.label} · {op?.label ?? c.operation}</div>
		{#if op}
			<div class="mono text-[0.62rem] text-[var(--indigo-200)]">Runs as the {op.activity} Activity</div>
		{:else}
			<div class="text-[0.68rem] text-[var(--red)]">{spec.label} has no operation "{c.operation}". Delete this step and add one from the palette.</div>
		{/if}
	</div>
	{#if op}

		{#each op.settings ?? [] as f (f.name)}
			{#if f.type === 'boolean'}
				<div class="space-y-1.5 border border-[var(--line)] p-2.5">
					<div class="flex items-center justify-between">
						<span class="text-sm">{f.label ?? f.name}</span>
						<Switch bind:checked={() => c.settings?.[f.name] === true, (v) => setSetting(f, v)} />
					</div>
					{#if f.description}{@render note(f.description)}{/if}
				</div>
			{:else}
				<Field label={(f.label ?? f.name) + (f.required ? '' : ' (optional)')} hint={patternError(f) || f.description}>
					<Input class="mono text-xs" bind:value={() => settingText(f), (v) => setSetting(f, v)} placeholder={f.placeholder} aria-invalid={!!patternError(f)} />
				</Field>
				{#if f.suggestions?.length}
					<div class="-mt-2 flex flex-wrap gap-1">
						{#each f.suggestions as s (s)}
							<button
								class="mono border border-dashed px-1.5 py-0.5 text-[0.62rem] text-[var(--slate-300)] hover:border-[var(--uv-light)]"
								style:border-color={settingText(f) === s ? accent : 'var(--line)'}
								onclick={() => setSetting(f, s)}>{s}</button
							>
						{/each}
					</div>
				{/if}
			{/if}
		{/each}

		{#if op.inputs?.length}
			<div class="space-y-2 border border-[var(--line)] p-2.5">
				<div class="flex items-center justify-between">
					<span class="eyebrow !text-[0.62rem]">Inputs</span>
					<span class="mono text-[0.6rem]" style:color={missing.length ? 'var(--red)' : 'var(--slate-500)'}>
						{missing.length ? `${missing.length} required to map` : 'all required mapped'}
					</span>
				</div>
				<Button size="sm" class="btn-uv mono w-full" onclick={() => (mapperOpen = true)}><Workflow /> Open the mapper</Button>
				{@render note('Map each input from the run: {{ input.company }}, {{ steps.lookup.id }}, or a fixed value. The Workflow builds the Activity\'s input from these, deterministically, and gives each value its field\'s type.')}
				{#each op.inputs as f (f.name)}
					<MappingField {spec} field={f} path={f.name} config={c} />
				{/each}
			</div>
		{/if}

		{#if outputs.length}
			<div class="space-y-1.5 border border-[var(--line)] p-2.5">
				<span class="eyebrow !text-[0.62rem]">Outputs</span>
				{@render note(`Later steps read these as {{ steps.${node.key}.… }}. Click one to copy it.`)}
				<OutputTree fields={outputs} base="steps.{node.key}" {accent} />
			</div>
		{/if}

		{#if op.note}{@render note(op.note)}{/if}
		{#if c.undo}
			<div class="space-y-1.5 border border-[var(--line)] p-2.5">
				<div class="flex items-center justify-between">
					<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem]"><Undo2 class="size-3" /> Undo (older)</span>
					<Switch bind:checked={() => !!c.undo, (v) => (c.undo = v || undefined)} />
				</div>
				{@render note('This step was saved with its rollback switched on for the whole run. Sagas replace that: add a Saga step and link this step to it (its built-in rollback comes along). Turn this off once you have.')}
			</div>
		{:else if op.compensation}
			{@render note(`Built-in rollback: ${op.compensation.note} Link this step to a Saga to use it.`)}
		{/if}
	{/if}
	<ActivitySettings bind:options={c.activity} {inherited} showStatusCodes={false} />
	{#if sharedLimits.length || c.rateLimit}
		<Field label="Shared rate limit" hint="A limit every run shares, to stay inside the service's API quota. Calls wait their turn on the limit's task queue, which Temporal dispatches no faster than the rate.">
			<Choice
				bind:value={() => c.rateLimit ?? '', (v) => (c.rateLimit = v || undefined)}
				options={[{ value: '', label: 'None' }, ...sharedLimits.map((l) => ({ value: l.name, label: `${l.name} — ${l.count} per ${l.per}` }))]}
			/>
		</Field>
	{/if}
	{@render note(spec.errors)}
{/if}

{#if mapperOpen && spec && op}
	<DataMapper {node} close={() => (mapperOpen = false)} />
{/if}
