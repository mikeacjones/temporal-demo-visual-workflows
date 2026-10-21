<script lang="ts">
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import ActivitySettings from './ActivitySettings.svelte';
	import { connectorSpec, operationDefaults } from '$lib/connectors';
	import type { ActivityOptions, ConnectorConfig, ConnectorParam, ConnectorType, FlowNode } from '$lib/types';

	// A business connector step's settings, drawn from its connector's spec
	// (internal/connectors): pick an operation, fill in its parameters.
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
	const output = $derived(op?.output.replaceAll('{out}', `steps.${node.key}`) ?? '');

	function choose(name: string) {
		const next = spec?.operations.find((o) => o.name === name);
		if (!next) return;
		// Keep what was typed into parameters both operations share.
		const kept = Object.fromEntries(next.params.filter((p) => c.params[p.key] !== undefined).map((p) => [p.key, c.params[p.key]]));
		c.params = { ...operationDefaults(next), ...kept };
		if (!next.undoNote) c.undo = undefined;
	}

	const text = (p: ConnectorParam) => (c.params[p.key] as string | undefined) ?? '';
	const setText = (p: ConnectorParam, v: string) => {
		if (v) c.params[p.key] = v;
		else delete c.params[p.key];
	};

	const jsonError = (s: string) => {
		if (!s.trim()) return '';
		try {
			JSON.parse(s);
			return '';
		} catch (e) {
			return (e as Error).message;
		}
	};
	const patternError = (p: ConnectorParam) => {
		const v = text(p);
		return p.pattern && v && !v.includes('{{') && !new RegExp(p.pattern).test(v) ? `Use ${p.patternHint}` : '';
	};
</script>

{#snippet note(s: string)}
	<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{s}</p>
{/snippet}

{#if !spec}
	{@render note('Loading the connector…')}
{:else}
	<div class="flex gap-2 border border-[var(--line)] p-2.5">
		<KeyRound class="mt-0.5 size-3.5 shrink-0" style="color: var(--{spec.type})" />
		{@render note(`${spec.connection} No credential is stored in the flow, the Activity's input, or the run's history.`)}
	</div>
	<Field label="Operation">
		<Choice bind:value={c.operation} options={spec.operations.map((o) => ({ value: o.name, label: o.label }))} onchange={choose} />
	</Field>
	{#if op}
		<div class="mono -mt-2 text-[0.62rem] text-[var(--indigo-200)]">Runs as the {op.activity} Activity</div>
		{#each op.params as p (p.key)}
			{#if p.kind === 'bool'}
				<div class="space-y-1.5 border border-[var(--line)] p-2.5">
					<div class="flex items-center justify-between">
						<span class="text-sm">{p.label}</span>
						<Switch bind:checked={() => c.params[p.key] === true, (v) => (v ? (c.params[p.key] = true) : delete c.params[p.key])} />
					</div>
					{#if p.hint}{@render note(p.hint)}{/if}
				</div>
			{:else}
				<Field label={p.label + (p.required ? '' : ' (optional)')} hint={patternError(p) || p.hint}>
					{#if p.kind === 'code' || p.kind === 'json'}
						<Textarea
							class="mono text-xs {p.kind === 'json' ? 'min-h-28' : 'min-h-20'}"
							bind:value={() => text(p), (v) => setText(p, v)}
							placeholder={p.placeholder}
							spellcheck={false}
							aria-invalid={p.kind === 'json' && !!jsonError(text(p))}
						/>
						{#if p.kind === 'json' && jsonError(text(p))}<p class="text-[0.68rem] text-[var(--red)]">{jsonError(text(p))}</p>{/if}
					{:else}
						<Input class="mono text-xs" bind:value={() => text(p), (v) => setText(p, v)} placeholder={p.placeholder} aria-invalid={!!patternError(p)} />
					{/if}
				</Field>
				{#if p.suggestions?.length}
					<div class="-mt-2 flex flex-wrap gap-1">
						{#each p.suggestions as s (s)}
							<button
								class="mono border border-dashed px-1.5 py-0.5 text-[0.62rem] text-[var(--slate-300)] hover:border-[var(--uv-light)]"
								style="border-color: {text(p) === s ? `var(--${spec.type})` : 'var(--line)'}"
								onclick={() => setText(p, s)}>{s}</button
							>
						{/each}
					</div>
				{/if}
			{/if}
		{/each}
		{#if op.note}{@render note(op.note)}{/if}
		{#if op.undoNote}
			<div class="space-y-1.5 border border-[var(--line)] p-2.5">
				<div class="flex items-center justify-between">
					<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem]"><Undo2 class="size-3" /> Undo</span>
					<Switch bind:checked={() => !!c.undo, (v) => (c.undo = v || undefined)} />
				</div>
				{@render note(op.undoNote + ' Undo steps run in reverse order, as a saga.')}
			</div>
		{/if}
		{@render note(`Later steps read ${output}.`)}
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
