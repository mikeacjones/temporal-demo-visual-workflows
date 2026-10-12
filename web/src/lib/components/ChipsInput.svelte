<script lang="ts">
	import X from '@lucide/svelte/icons/x';

	// A list of short values as removable chips, with optional suggestions.
	let {
		values = $bindable(),
		suggestions = [],
		placeholder = 'Add…',
		numeric = false
	}: { values: (string | number)[] | undefined; suggestions?: string[]; placeholder?: string; numeric?: boolean } = $props();

	let draft = $state('');
	const list = $derived(values ?? []);
	const remaining = $derived(suggestions.filter((s) => !list.map(String).includes(s)));

	function add(raw: string) {
		const v = raw.trim();
		if (!v) return;
		const item = numeric ? Number(v) : v;
		if (numeric && !Number.isInteger(item)) return;
		if (!list.map(String).includes(String(item))) values = [...list, item] as typeof values;
		draft = '';
	}
	function remove(i: number) {
		const next = list.filter((_, j) => j !== i);
		values = (next.length ? next : undefined) as typeof values;
	}
</script>

<div class="space-y-1.5">
	<div class="flex min-h-8 flex-wrap items-center gap-1 border border-[var(--input)] px-1.5 py-1">
		{#each list as v, i (String(v))}
			<span class="mono flex items-center gap-1 border border-[var(--line)] bg-[#1f2233] px-1.5 py-0.5 text-[0.68rem]">
				{v}<button onclick={() => remove(i)} aria-label="Remove {v}" class="text-[var(--slate-300)] hover:text-[var(--off-white)]"><X class="size-3" /></button>
			</span>
		{/each}
		<input
			class="mono min-w-20 flex-1 bg-transparent px-1 text-[0.72rem] outline-none"
			bind:value={draft}
			{placeholder}
			inputmode={numeric ? 'numeric' : undefined}
			onkeydown={(e) => {
				if (e.key === 'Enter' || e.key === ',') {
					e.preventDefault();
					add(draft);
				} else if (e.key === 'Backspace' && !draft && list.length) remove(list.length - 1);
			}}
			onblur={() => add(draft)}
		/>
	</div>
	{#if remaining.length}
		<div class="flex flex-wrap gap-1">
			{#each remaining as s (s)}
				<button class="mono border border-dashed border-[var(--line)] px-1.5 py-0.5 text-[0.62rem] text-[var(--slate-300)] hover:border-[var(--uv-light)] hover:text-[var(--off-white)]" onclick={() => add(s)}>+ {s}</button>
			{/each}
		</div>
	{/if}
</div>
