<script lang="ts">
	import Send from '@lucide/svelte/icons/send';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import Field from './Field.svelte';
	import { api } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import { clock } from '$lib/status';
	let {
		signalName,
		until,
		waiting = true,
		initialPayload
	}: { signalName: string; until?: string; waiting?: boolean; initialPayload?: string } = $props();
	let name = $state('');
	let payload = $state('{\n  "ok": true\n}');
	let seeded = false;
	// Start from the last payload received, so a presenter only edits the field that changes.
	$effect(() => {
		if (!seeded && initialPayload) {
			payload = initialPayload;
			seeded = true;
		}
	});
	let busy = $state(false);

	$effect(() => {
		name = signalName;
	});

	async function send() {
		if (!app.selectedRunId) return;
		let body: unknown;
		try {
			body = payload.trim() ? JSON.parse(payload) : undefined;
		} catch (err) {
			toast.error(`Payload is not valid JSON: ${(err as Error).message}`);
			return;
		}
		busy = true;
		try {
			await api.signal(app.selectedRunId, name, body);
			toast.success(`Signal "${name}" sent`);
			await app.refreshRun();
		} catch (err) {
			toast.error(errorMessage(err));
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-3 border border-[var(--purple)] bg-[var(--purple)]/10 p-3">
	<div class="eyebrow !text-[var(--purple)]">{waiting ? 'Send the event it is waiting for' : 'Send an event'}</div>
	{#if until}<p class="mono text-[0.68rem] text-[var(--slate-300)]">Timeout timer fires at {clock(until)}</p>{/if}
	<Field label="Signal name"><Input class="mono" bind:value={name} /></Field>
	<Field label="Payload (JSON)"><Textarea class="mono min-h-20 text-xs" bind:value={payload} spellcheck={false} /></Field>
	<Button class="btn-uv w-full" disabled={busy || !name} onclick={send}><Send /> Send signal</Button>
	<p class="mono text-[0.62rem] break-all text-[var(--slate-300)]">
		temporal workflow signal --workflow-id {app.selectedRunId} --name {name} --input '…'
	</p>
</div>
