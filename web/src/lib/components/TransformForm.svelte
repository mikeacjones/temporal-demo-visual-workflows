<script lang="ts">
	import Waypoints from '@lucide/svelte/icons/waypoints';
	import * as ToggleGroup from '$lib/components/ui/toggle-group';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import Field from './Field.svelte';
	import OutputTree from './OutputTree.svelte';
	import DataMapper from './mapper/DataMapper.svelte';
	import { confirmAction } from '$lib/confirm.svelte';
	import { infer } from '$lib/datasources';
	import { fromTemplate, missingMapped, toTemplate } from '$lib/mapping';
	import type { FlowNode, TransformConfig } from '$lib/types';

	// A Transform step: map typed output fields in the mapper (each field from
	// the run's data, lists element by element), or write a JSON template.
	// Either way the Workflow renders it, deterministically.
	let { node }: { node: FlowNode } = $props();

	const c = $derived(node.config as TransformConfig);
	const mapped = $derived(!!c.fields);
	const missing = $derived(mapped ? missingMapped(c) : []);
	let mapperOpen = $state(false);
	let sample = $state('');
	let sampleError = $state('');

	async function setMode(mode: string) {
		if (mode === 'mapper' && !mapped) {
			// Carry the template's fields over, each mapped as it was.
			const from = fromTemplate(c.output ?? '');
			c.fields = from?.fields ?? [];
			c.mapping = from?.mapping ?? {};
			c.lists = {};
			delete c.output;
		} else if (mode === 'template' && mapped) {
			if (Object.keys(c.lists ?? {}).length && !(await confirmAction({ title: 'Switch to a JSON template?', description: 'Lists mapped element by element can\'t be written as a template, and are dropped.', confirmLabel: 'Switch' })))
				return;
			c.output = toTemplate(c);
			delete c.fields;
			delete c.mapping;
			delete c.lists;
		}
	}

	function useSample() {
		try {
			const tree = infer('', JSON.parse(sample));
			if (tree.type !== 'object') throw new Error('Paste a JSON object');
			const known = new Set((c.fields ?? []).map((f) => f.name));
			c.fields = [...(c.fields ?? []), ...(tree.fields ?? []).filter((f) => !known.has(f.name))];
			sample = '';
			sampleError = '';
		} catch (e) {
			sampleError = (e as Error).message;
		}
	}

	const jsonError = (text: string | undefined) => {
		if (!text?.trim()) return '';
		try {
			JSON.parse(text);
			return '';
		} catch (e) {
			return (e as Error).message;
		}
	};
</script>

<ToggleGroup.Root type="single" value={mapped ? 'mapper' : 'template'} onValueChange={(v) => v && setMode(v)} variant="outline" size="sm" class="w-full">
	<ToggleGroup.Item value="mapper" class="mono flex-1 text-xs">Mapper</ToggleGroup.Item>
	<ToggleGroup.Item value="template" class="mono flex-1 text-xs">JSON template</ToggleGroup.Item>
</ToggleGroup.Root>

{#if mapped}
	<div class="space-y-2 border border-[var(--line)] p-2.5">
		<div class="flex items-center justify-between">
			<span class="eyebrow !text-[0.62rem]">Output</span>
			<span class="mono text-[0.6rem]" style:color={missing.length ? 'var(--red)' : 'var(--slate-500)'}>
				{missing.length ? `${missing.length} required to map` : `${c.fields?.length ?? 0} fields`}
			</span>
		</div>
		<Button size="sm" class="btn-uv mono w-full" onclick={() => (mapperOpen = true)}><Waypoints /> Open the mapper</Button>
		{#if c.fields?.length}
			<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">Later steps read these as {'{{'} steps.{node.key}.… {'}}'}. Click one to copy it.</p>
			<OutputTree fields={c.fields} base="steps.{node.key}" accent="var(--indigo-200)" />
		{:else}
			<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">No output fields yet. In the mapper, add them one by one, or drop a whole object onto Output to copy its fields, each mapped.</p>
		{/if}
	</div>
	<Field label="Fields from a sample (JSON)" hint="Paste an example of the output you want; its fields are added, typed from the values. Then map each in the mapper.">
		<Textarea class="mono min-h-16 text-xs" bind:value={sample} spellcheck={false} placeholder={'{"orderId": "A-1", "total": 12.5, "lines": [{"sku": "X", "qty": 1}]}'} />
	</Field>
	{#if sampleError}<p class="-mt-2 text-[0.68rem] text-[var(--red)]">{sampleError}</p>{/if}
	{#if sample.trim()}<Button variant="outline" size="xs" class="mono" onclick={useSample}>Add these fields</Button>{/if}
	<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
		The Workflow builds the output from the mapping in its own code, deterministically, giving each field its type and building lists element by element.
	</p>
{:else}
	<Field label="Output (JSON template)" hint={'String values can be expressions: {{ steps.lookup.body.email }}'}>
		<Textarea class="mono min-h-40 text-xs" bind:value={() => c.output ?? '', (v) => (c.output = v)} spellcheck={false} aria-invalid={!!jsonError(c.output)} />
		{#if jsonError(c.output)}<p class="text-[0.68rem] text-[var(--red)]">{jsonError(c.output)}</p>{/if}
	</Field>
{/if}

{#if mapperOpen}
	<DataMapper {node} close={() => (mapperOpen = false)} />
{/if}
