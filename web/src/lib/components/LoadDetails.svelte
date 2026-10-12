<script lang="ts">
	import Download from '@lucide/svelte/icons/download';
	import { Button } from '$lib/components/ui/button';
	import { app } from '$lib/state.svelte';
	import { shell } from '$lib/shell.svelte';

	// Outputs aren't in the run's progress memo; reading them is a Query (or the
	// result, once the run has finished), so it's an explicit action.
	let { label = 'Load output' }: { label?: string } = $props();
</script>

{#if !app.details}
	<div class="space-y-1">
		<Button variant="outline" size="xs" class="mono" disabled={app.detailsLoading} onclick={() => app.loadDetails()}>
			<Download />
			{app.detailsLoading ? 'Loading…' : label}
		</Button>
		{#if shell.serverless && app.runActive}
			<p class="text-[0.62rem] leading-snug text-[var(--slate-300)]">Reads live state — may wake a worker.</p>
		{/if}
	</div>
{/if}
