<script lang="ts">
	import { app } from '$lib/state.svelte';
	import { pretty } from '$lib/status';

	// The run's variables, from the progress memo (no worker needed).
	const vars = $derived(app.lastState?.vars ?? {});
	const declared = $derived(app.lastState?.flow.settings?.variables ?? []);
	const names = $derived([...new Set([...declared.map((v) => v.name), ...Object.keys(vars)])].sort());
</script>

{#if names.length}
	<div class="space-y-1.5">
		<div class="eyebrow !text-[0.62rem]">Variables</div>
		<div class="divide-y divide-[var(--line)] border border-[var(--line)]">
			{#each names as name (name)}
				{@const def = declared.find((d) => d.name === name)}
				<div class="flex items-start gap-2 px-2 py-1.5 text-xs">
					<span class="mono shrink-0 text-[var(--indigo-100)]">vars.{name}</span>
					{#if def?.visible}<span class="mono text-[0.55rem] text-[var(--green)] uppercase" data-tip="Readable from outside without touching history">visible</span>{/if}
					<span class="mono ml-auto min-w-0 truncate text-right text-[var(--off-white)]" data-tip={pretty(vars[name])}>
						{name in vars ? JSON.stringify(vars[name]) : '—'}
					</span>
				</div>
			{/each}
		</div>
	</div>
{/if}
