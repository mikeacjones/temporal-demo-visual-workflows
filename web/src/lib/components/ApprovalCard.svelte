<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import X from '@lucide/svelte/icons/x';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { api } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import { clock } from '$lib/status';
	import type { Waiting } from '$lib/types';

	// item: for a step in a For each body, the item being decided.
	let { nodeId, waiting, item }: { nodeId: string; waiting: Waiting; item?: number } = $props();
	let comment = $state('');
	let busy = $state(false);

	async function decide(approved: boolean) {
		if (!app.selectedRunId) return;
		busy = true;
		try {
			await api.approve(app.selectedRunId, nodeId, approved, comment || undefined, item);
			toast.success(approved ? 'Approved — the Update was accepted' : 'Rejected — the Update was accepted');
			await app.refreshRun();
			if (item !== undefined) await app.loadDetails(); // the item's own steps
		} catch (err) {
			toast.error(errorMessage(err));
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-3 border border-[var(--purple)] bg-[var(--purple)]/10 p-3">
	<div class="eyebrow !text-[var(--purple)]">Decision needed</div>
	<div class="text-[0.95rem] font-semibold">{waiting.title}</div>
	{#if waiting.details}<p class="text-sm whitespace-pre-wrap text-[var(--indigo-100)]">{waiting.details}</p>{/if}
	{#if waiting.until}<p class="mono text-[0.68rem] text-[var(--slate-300)]">Times out at {clock(waiting.until)} (routes to timeout, not rejected)</p>{/if}
	<Input bind:value={comment} placeholder="Comment (optional)" />
	<div class="flex gap-2">
		<Button class="btn-green flex-1" disabled={busy} onclick={() => decide(true)}><Check /> Approve</Button>
		<Button variant="outline" class="mono flex-1" disabled={busy} onclick={() => decide(false)}><X /> Reject</Button>
	</div>
	<p class="text-[0.66rem] leading-snug text-[var(--slate-300)]">
		Sent as a Workflow Update. Its validator rejects decisions for steps that are not waiting, so a double click cannot approve twice.
	</p>
</div>
