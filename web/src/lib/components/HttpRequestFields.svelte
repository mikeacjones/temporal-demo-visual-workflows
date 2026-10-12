<script lang="ts">
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Button } from '$lib/components/ui/button';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import type { HTTPRequestSpec } from '$lib/types';

	// Method, URL, headers and body. Used by the HTTP step and its undo request.
	let { req = $bindable() }: { req: HTTPRequestSpec } = $props();

	const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({ value: m, label: m }));
	// Rows keep their order and allow a key being typed to be temporarily blank.
	let rows = $state<{ k: string; v: string }[]>(Object.entries(req.headers ?? {}).map(([k, v]) => ({ k, v })));
	export function setHeader(k: string, v: string) {
		const existing = rows.find((r) => r.k === k);
		if (existing) existing.v = v;
		else rows.push({ k, v });
		sync();
	}
	function sync() {
		const h: Record<string, string> = {};
		for (const r of rows) if (r.k.trim()) h[r.k.trim()] = r.v;
		req.headers = h;
	}
</script>

<div class="grid grid-cols-[96px_1fr] gap-2">
	<Field label="Method"><Choice bind:value={req.method} options={methods} /></Field>
	<Field label="URL"><Input class="mono text-xs" bind:value={req.url} /></Field>
</div>
<Field label="Headers">
	<div class="space-y-1.5">
		{#each rows as row, i (i)}
			<div class="flex gap-1.5">
				<Input class="mono text-xs" bind:value={row.k} oninput={sync} placeholder="Header" />
				<Input class="mono text-xs" bind:value={row.v} oninput={sync} placeholder="value or {'{{ … }}'}" />
				<Button variant="ghost" size="icon-sm" onclick={() => (rows.splice(i, 1), sync())}><X /></Button>
			</div>
		{/each}
		<Button variant="outline" size="xs" class="mono" onclick={() => rows.push({ k: '', v: '' })}><Plus /> header</Button>
	</div>
</Field>
{#if req.method !== 'GET' && req.method !== 'DELETE'}
	<Field label="Body" hint={'Templates allowed: {{ steps.order.body.id }}'}>
		<Textarea class="mono min-h-24 text-xs" bind:value={req.body} spellcheck={false} />
	</Field>
{/if}
