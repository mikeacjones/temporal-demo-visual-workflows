<script lang="ts">
	import RotateCw from '@lucide/svelte/icons/rotate-cw';
	import X from '@lucide/svelte/icons/x';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { api } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import type { Waiting } from '$lib/types';

	// A step paused after its retries ran out (a resumable Activity). The
	// person corrects the request and retries, or lets the step fail.
	let { nodeId, waiting }: { nodeId: string; waiting: Waiting } = $props();
	let url = $state('');
	let body = $state('');
	let busy = $state(false);
	$effect(() => {
		url = waiting.request?.url ?? '';
		body = waiting.request?.body ?? '';
	});

	async function send(action: 'retry' | 'fail') {
		if (!app.selectedRunId) return;
		busy = true;
		try {
			const change: { url?: string; body?: string } = {};
			if (url !== (waiting.request?.url ?? '')) change.url = url;
			if (body !== (waiting.request?.body ?? '')) change.body = body;
			await api.fix(app.selectedRunId, nodeId, action, action === 'retry' ? change : {});
			toast.success(action === 'retry' ? 'Retrying with your fix — the Update was accepted' : 'Letting the step fail');
			await app.refreshRun();
		} catch (err) {
			toast.error(errorMessage(err));
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-3 border border-[var(--pink)] bg-[var(--pink)]/10 p-3">
	<div class="eyebrow !text-[var(--pink)]">Retries ran out · paused for a fix</div>
	<pre class="json max-h-24 overflow-auto !text-[var(--pink)]">{waiting.details}</pre>
	<div class="space-y-1">
		<div class="eyebrow !text-[0.58rem]">{waiting.request?.method ?? 'URL'}</div>
		<Input class="mono text-xs" bind:value={url} />
	</div>
	{#if waiting.request?.body !== undefined && waiting.request?.body !== ''}
		<div class="space-y-1">
			<div class="eyebrow !text-[0.58rem]">Body</div>
			<Textarea class="mono min-h-20 text-xs" bind:value={body} spellcheck={false} />
		</div>
	{/if}
	<div class="flex gap-2">
		<Button class="btn-green flex-1" disabled={busy} onclick={() => send('retry')}><RotateCw /> Retry</Button>
		<Button variant="outline" class="mono flex-1" disabled={busy} onclick={() => send('fail')}><X /> Let it fail</Button>
	</div>
	<p class="text-[0.66rem] leading-snug text-[var(--slate-300)]">
		The run is parked with nothing running. Retry sends a fix_step Update; the step runs again with a fresh retry policy, and history records exactly what changed.
	</p>
</div>
