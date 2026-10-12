<script lang="ts">
	import Send from '@lucide/svelte/icons/send';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import Field from './Field.svelte';
	import { api, ApiError } from '$lib/api';
	import { app, errorMessage } from '$lib/state.svelte';
	import { pretty } from '$lib/status';
	import { updateUrl } from '$lib/progress';

	// Sends a Workflow Update to an On update lane and shows what came back.
	let { updateName, validator }: { updateName: string; validator?: string } = $props();

	let payload = $state('{\n  \n}');
	let busy = $state(false);
	let result = $state<{ ok: boolean; value: unknown } | null>(null);

	async function send() {
		const wid = app.selectedRunId;
		if (!wid) return;
		let body: unknown;
		try {
			body = payload.trim() ? JSON.parse(payload) : undefined;
		} catch (err) {
			result = { ok: false, value: `Payload is not valid JSON: ${(err as Error).message}` };
			return;
		}
		busy = true;
		try {
			const res = await api.update(wid, updateName, body);
			result = { ok: true, value: res?.result };
			await app.refreshRun();
		} catch (err) {
			result = { ok: false, value: err instanceof ApiError ? err.message : errorMessage(err) };
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-3 border border-[var(--purple)] bg-[var(--purple)]/10 p-3">
	<div class="eyebrow !text-[var(--purple)]">Send Update "{updateName}"</div>
	{#if validator}
		<p class="mono text-[0.66rem] text-[var(--slate-300)]">Accepted only if: <span class="text-[var(--yellow)]">{validator}</span></p>
	{/if}
	<Field label="Request (JSON)"><Textarea class="mono min-h-20 text-xs" bind:value={payload} spellcheck={false} /></Field>
	<Button class="btn-uv w-full" disabled={busy || !app.runActive} onclick={send}><Send /> Send update</Button>
	{#if result}
		<div class="space-y-1">
			<div class="eyebrow !text-[0.6rem]" style:color={result.ok ? 'var(--green)' : 'var(--pink)'}>{result.ok ? 'Result' : 'Not accepted'}</div>
			<pre class="json max-h-48 overflow-auto" style:color={result.ok ? undefined : 'var(--pink)'}>{pretty(result.value)}</pre>
		</div>
	{/if}
	{#if app.runView?.callbackUrl && app.runActive}
		<p class="mono text-[0.6rem] break-all text-[var(--slate-300)]">
			Outside systems: POST {updateUrl(app.runView.callbackUrl, updateName)}
		</p>
	{/if}
	<p class="text-[0.64rem] text-[var(--slate-300)]">A rejected Update leaves no trace in the run's history.</p>
</div>
