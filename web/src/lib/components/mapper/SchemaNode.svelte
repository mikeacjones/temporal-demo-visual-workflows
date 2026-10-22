<script lang="ts" module>
	import type { Node } from '@xyflow/svelte';

	/** One row of a schema tree: a field, with a handle the mapping lines attach to. */
	export interface Row {
		/** The handle's ID: a source path (steps.account.id) or an input path (values.Name). */
		path: string;
		depth: number;
		name: string;
		/** A top-level source's title: the step's label. */
		title?: string;
		type: string;
		accent?: string;
		required?: boolean;
		description?: string;
		expandable?: boolean;
		expanded?: boolean;
		/** Target rows: how the field is mapped, and its sample value. */
		state?: 'direct' | 'expression' | 'constant' | '';
		template?: string;
		preview?: string;
		error?: string;
		custom?: boolean;
		/** A row that adds a field to an object: a custom field, or an output field. */
		add?: boolean;
		/** A list mapped element by element: the source list it comes from. */
		list?: string;
		/** Accepts a whole object dropped on it (a Transform's output root). */
		root?: boolean;
	}

	export type SchemaData = {
		side: 'source' | 'target';
		title: string;
		subtitle: string;
		accent: string;
		rows: Row[];
		selected: string | null;
		toggle: (path: string) => void;
		select: (path: string) => void;
		addField: (parent: string, name: string) => void;
		/** What an add row's input says: Custom field, or Output field. */
		addLabel: string;
		removeField: (path: string) => void;
	};
	export type SchemaNodeType = Node<SchemaData, 'schema'>;
</script>

<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';

	let { data }: NodeProps<SchemaNodeType> = $props();
	const source = $derived(data.side === 'source');
	let adding = $state<Record<string, string>>({});

	function add(row: Row) {
		const name = (adding[row.path] ?? '').trim();
		if (!name) return;
		data.addField(row.path, name);
		adding[row.path] = '';
	}
</script>

<div class="w-[340px] border bg-[#15171f] shadow-[0_8px_24px_-12px_rgba(0,0,0,.8)]" style:border-color={data.accent}>
	<div class="border-b border-[var(--line)] px-3 py-2">
		<div class="eyebrow !text-[0.6rem]" style:color={data.accent}>{data.title}</div>
		<div class="mono text-[0.62rem] text-[var(--slate-300)]">{data.subtitle}</div>
	</div>
	<div class="py-1">
		{#each data.rows as row (row.path + (row.add ? ':add' : ''))}
			{#if row.add}
				<div class="nodrag flex items-center gap-1 py-0.5 pr-2" style:padding-left="{10 + row.depth * 14}px">
					<input
						class="mono h-6 min-w-0 flex-1 border border-[var(--line)] bg-transparent px-1.5 text-[0.66rem] outline-none focus:border-[var(--uv-light)]"
						placeholder={data.addLabel}
						bind:value={adding[row.path]}
						onkeydown={(e) => e.key === 'Enter' && add(row)}
					/>
					<button class="grid size-6 place-items-center border border-[var(--line)] hover:border-[var(--uv-light)]" onclick={() => add(row)} aria-label="Add a custom field"><Plus class="size-3" /></button>
				</div>
			{:else}
				<div
					class="group relative flex cursor-pointer items-center gap-1.5 py-[3px] pr-3 hover:bg-[var(--accent)]"
					class:bg-[var(--accent)]={data.selected === row.path}
					style:padding-left="{10 + row.depth * 14}px"
					role="button"
					tabindex="0"
					onclick={() => (row.expandable && source ? data.toggle(row.path) : data.select(row.path))}
					onkeydown={(e) => e.key === 'Enter' && data.select(row.path)}
					data-tip={row.error ?? row.description}
				>
					{#if row.expandable}
						<button
							class="nodrag -ml-1 text-[var(--slate-300)]"
							onclick={(e) => (e.stopPropagation(), data.toggle(row.path))}
							aria-label={row.expanded ? 'Collapse' : 'Expand'}
						>
							{#if row.expanded}<ChevronDown class="size-3" />{:else}<ChevronRight class="size-3" />{/if}
						</button>
					{:else}
						<span class="inline-block w-2"></span>
					{/if}
					{#if row.title}
						<span class="inline-block size-2 shrink-0" style:background={row.accent}></span>
						<span class="truncate text-[0.74rem] font-semibold">{row.title}</span>
						<span class="mono truncate text-[0.6rem] text-[var(--slate-500)]">{row.name}</span>
					{:else}
						<span class="mono shrink-0 text-[0.7rem]" class:text-[var(--off-white)]={!!row.state}>{row.name}</span>
						{#if row.required}<span class="text-[0.7rem] text-[var(--red)]">*</span>{/if}
						{#if row.custom}
							<span class="mono text-[0.55rem] text-[var(--slate-500)]">custom</span>
							<button class="nodrag text-[var(--slate-500)] opacity-0 group-hover:opacity-100 hover:text-[var(--red)]" onclick={(e) => (e.stopPropagation(), data.removeField(row.path))} aria-label="Remove {row.name}"><X class="size-3" /></button>
						{/if}
					{/if}
					<span class="ml-auto flex min-w-0 items-center gap-1.5">
						{#if !source && row.list}
							<span class="mono max-w-[110px] truncate border border-[var(--cyan)]/60 px-1 text-[0.58rem] text-[var(--cyan)]" data-tip="Each element of {row.list}">each of {row.list.split('.').at(-1)}</span>
						{:else if !source && row.state === 'constant'}
							<span class="mono max-w-[120px] truncate border border-[var(--line)] px-1 text-[0.58rem] text-[var(--yellow)]">{row.template}</span>
						{:else if !source && row.state === 'expression'}
							<span class="mono text-[0.62rem] text-[var(--purple)]" title={row.template}>ƒ</span>
						{/if}
						{#if !source && row.error}
							<span class="mono text-[0.58rem] text-[var(--red)]">error</span>
						{:else if !source && row.preview !== undefined}
							<span class="mono max-w-[110px] truncate text-[0.58rem] text-[var(--green)]">{row.preview}</span>
						{/if}
						<span class="mono shrink-0 text-[0.56rem] text-[var(--slate-500)]">{row.type}</span>
					</span>
					<Handle
						type={source ? 'source' : 'target'}
						position={source ? Position.Right : Position.Left}
						id={row.path}
						class="!size-2 !rounded-none !border !bg-[#15171f]"
						style="border-color: {row.state || source ? (row.accent ?? data.accent) : 'var(--slate-500)'}"
					/>
				</div>
			{/if}
		{/each}
	</div>
</div>
