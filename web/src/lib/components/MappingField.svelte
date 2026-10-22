<script lang="ts">
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import Self from './MappingField.svelte';
	import { catalogKnows, isMapped, resolveField, typeLabel } from '$lib/connectors';
	import type { ConnectorConfig, ConnectorField, ConnectorSpec } from '$lib/types';

	// One input of a connector step and how it's mapped: a template for a
	// value, or, for an object, its fields in turn.
	let { spec, field, path, config }: { spec: ConnectorSpec; field: ConnectorField; path: string; config: ConnectorConfig } = $props();

	const f = $derived(resolveField(spec, field, config.settings));
	const value = $derived(config.mapping?.[path] ?? '');
	const accent = $derived(`var(--${spec.type})`);

	function set(p: string, v: string) {
		config.mapping ??= {};
		if (v) config.mapping[p] = v;
		else delete config.mapping[p];
	}

	// An object's fields: the required and mapped ones first; the rest wait
	// behind "more fields" when there are many (a Salesforce object has dozens).
	let expanded = $state(true);
	let showAll = $state(false);
	const known = $derived((f.fields ?? []).filter((x) => !x.readOnly));
	const shown = $derived(known.filter((x) => x.required || isMapped(config, `${path}.${x.name}`)));
	const rest = $derived(known.filter((x) => !shown.includes(x)));
	const custom = $derived(
		Object.keys(config.mapping ?? {})
			.filter((k) => k.startsWith(path + '.') && !k.slice(path.length + 1).includes('.'))
			.map((k) => k.slice(path.length + 1))
			.filter((name) => !known.some((x) => x.name === name))
	);
	let newField = $state('');
	function addCustom() {
		const name = newField.trim();
		if (!name) return;
		set(`${path}.${name}`, config.mapping?.[`${path}.${name}`] || ' ');
		newField = '';
	}

	const jsonError = $derived.by(() => {
		if (f.type !== 'json' || !value.trim() || value.includes('{{')) return '';
		try {
			JSON.parse(value);
			return '';
		} catch (e) {
			return (e as Error).message;
		}
	});
	const patternError = $derived(f.pattern && value && !value.includes('{{') && !new RegExp(f.pattern).test(value) ? `Use ${f.patternHint}` : '');
	const enumError = $derived(f.enum?.length && value && !value.includes('{{') && !f.enum.includes(value) ? `Not one of its values` : '');
	const choices = $derived(f.enum?.map((v, i) => ({ value: v, label: f.enumLabels?.[i] ?? v })) ?? f.suggestions?.map((v) => ({ value: v, label: v })) ?? []);
</script>

{#snippet head(f: ConnectorField, mapped: boolean)}
	<span class="mono text-[0.72rem] text-[var(--off-white)]">{f.name}</span>
	{#if f.required}<span class="text-[var(--red)]" data-tip="Required">*</span>{/if}
	{#if f.label && f.label !== f.name}<span class="truncate text-[0.66rem] text-[var(--slate-300)]">{f.label}</span>{/if}
	<span class="mono ml-auto shrink-0 text-[0.58rem]" style:color={mapped ? accent : 'var(--slate-500)'}>{typeLabel(f)}</span>
{/snippet}

{#if f.type === 'object'}
	<div class="border-l pl-2" style:border-color={isMapped(config, path) ? accent : 'var(--line)'}>
		<button class="flex w-full items-center gap-1.5 py-0.5 text-left" onclick={() => (expanded = !expanded)} data-tip={f.description}>
			{#if expanded}<ChevronDown class="size-3 shrink-0" />{:else}<ChevronRight class="size-3 shrink-0" />{/if}
			{@render head(f, isMapped(config, path))}
		</button>
		{#if expanded}
			<div class="mt-1 space-y-2 pb-1">
				{#if f.catalog && !catalogKnows(spec, f, config.settings)}
					<p class="text-[0.66rem] text-[var(--slate-300)]">
						{config.settings?.[f.catalog] || 'This'} isn't in the curated {spec.catalogOf ?? 'catalog'} list: add its fields by API name.
					</p>
				{/if}
				{#each shown as sub (sub.name)}
					<Self {spec} field={sub} path="{path}.{sub.name}" {config} />
				{/each}
				{#each custom as name (name)}
					<div class="space-y-1">
						<div class="flex items-center gap-1.5">
							<span class="mono text-[0.72rem]">{name}</span>
							<span class="mono text-[0.58rem] text-[var(--slate-500)]">custom</span>
							<button class="ml-auto text-[var(--slate-500)] hover:text-[var(--red)]" onclick={() => set(`${path}.${name}`, '')} aria-label="Remove {name}"><X class="size-3" /></button>
						</div>
						<Input class="mono h-7 text-xs" bind:value={() => (config.mapping?.[`${path}.${name}`] ?? '').trimStart(), (v) => set(`${path}.${name}`, v || ' ')} placeholder={'{{ … }}'} />
					</div>
				{/each}
				{#if rest.length}
					{#if showAll}
						{#each rest as sub (sub.name)}
							<Self {spec} field={sub} path="{path}.{sub.name}" {config} />
						{/each}
					{/if}
					<button class="mono text-[0.64rem] text-[var(--indigo-200)] hover:underline" onclick={() => (showAll = !showAll)}>
						{showAll ? 'Hide unmapped fields' : `${rest.length} more fields`}
					</button>
				{/if}
				{#if f.open}
					<div class="flex gap-1.5">
						<Input class="mono h-7 text-xs" bind:value={newField} placeholder={f.catalog ? 'Custom field, e.g. Region__c' : 'Key'} onkeydown={(e) => e.key === 'Enter' && addCustom()} />
						<button class="grid size-7 shrink-0 place-items-center border border-[var(--line)] hover:border-[var(--uv-light)]" onclick={addCustom} aria-label="Add field"><Plus class="size-3" /></button>
					</div>
				{/if}
			</div>
		{/if}
	</div>
{:else}
	<div class="space-y-1">
		<div class="flex items-center gap-1.5" data-tip={f.description}>{@render head(f, !!value.trim())}</div>
		{#if f.multiline || f.type === 'json'}
			<Textarea
				class="mono text-xs {f.type === 'json' ? 'min-h-24' : 'min-h-16'}"
				bind:value={() => value, (v) => set(path, v)}
				placeholder={f.placeholder ?? '{{ … }}'}
				spellcheck={false}
				aria-invalid={!!jsonError}
			/>
		{:else}
			<Input class="mono h-7 text-xs" bind:value={() => value, (v) => set(path, v)} placeholder={f.placeholder ?? '{{ … }}'} aria-invalid={!!(patternError || enumError)} />
		{/if}
		{#if choices.length}
			<div class="flex flex-wrap gap-1">
				{#each choices as c (c.value)}
					<button
						class="mono border border-dashed px-1.5 py-0.5 text-[0.6rem] text-[var(--slate-300)] hover:border-[var(--uv-light)]"
						style:border-color={value === c.value ? accent : 'var(--line)'}
						onclick={() => set(path, c.value)}>{c.label}</button
					>
				{/each}
			</div>
		{/if}
		{#if jsonError || patternError || enumError}
			<p class="text-[0.64rem] text-[var(--red)]">{jsonError || patternError || enumError}</p>
		{:else if f.description}
			<p class="text-[0.64rem] leading-snug text-[var(--slate-300)]">{f.description}</p>
		{/if}
	</div>
{/if}
