<script lang="ts">
	import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
	import * as Sheet from '$lib/components/ui/sheet';
	import * as Dialog from '$lib/components/ui/dialog';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { Button } from '$lib/components/ui/button';
	import MiniFlow from './MiniFlow.svelte';
	import { api } from '$lib/api';
	import { app, errorMessage, normalizeFlow } from '$lib/state.svelte';
	import { ago } from '$lib/status';
	import type { Definition, FlowVersion } from '$lib/types';

	let versions = $state<FlowVersion[]>([]);
	let error = $state('');
	let preview = $state<Definition | null>(null);
	let previewOpen = $state(false);

	$effect(() => {
		if (!app.historyOpen || !app.flow) return;
		const id = app.flow.id;
		api.versions(id)
			.then((v) => {
				// The API lists earlier versions; the current one comes from the editor.
				const list = v ?? [];
				const cur = app.flow;
				if (cur && !list.some((x) => x.version === cur.version)) list.unshift({ version: cur.version, updatedAt: cur.updatedAt, nodeCount: cur.nodes.length });
				versions = list;
				error = '';
			})
			.catch((e) => (error = errorMessage(e)));
	});

	async function open(v: number) {
		if (!app.flow) return;
		if (v === app.flow.version) {
			preview = normalizeFlow($state.snapshot(app.flow));
			previewOpen = true;
			return;
		}
		try {
			preview = normalizeFlow(await api.version(app.flow.id, v));
			previewOpen = true;
		} catch (e) {
			error = errorMessage(e);
		}
	}

	function restore() {
		if (!preview) return;
		app.restoreVersion(preview);
		previewOpen = false;
		app.historyOpen = false;
	}
</script>

<Sheet.Root bind:open={app.historyOpen}>
	<Sheet.Content side="right" class="w-[360px] !max-w-[360px] gap-0 p-0">
		<Sheet.Header class="border-b border-[var(--line)] p-4">
			<Sheet.Title>Version history</Sheet.Title>
			<Sheet.Description>Every save is a version. Runs keep the version they started with.</Sheet.Description>
		</Sheet.Header>
		<ScrollArea class="min-h-0 flex-1">
			<div class="space-y-1 p-3">
				{#if error}<p class="text-xs text-[var(--red)]">{error}</p>{/if}
				{#each versions as v (v.version)}
					<button class="flex w-full items-center gap-3 border border-[var(--line)] px-3 py-2 text-left hover:border-[var(--uv-light)]" onclick={() => open(v.version)}>
						<span class="mono text-sm font-semibold" style:color={v.version === app.flow?.version ? 'var(--green)' : undefined}>v{v.version}</span>
						<span class="text-xs text-[var(--slate-300)]">{v.nodeCount} steps · {ago(v.updatedAt)}</span>
						{#if v.version === app.flow?.version}<span class="status-badge ml-auto text-[var(--green)]">current</span>{/if}
					</button>
				{:else}
					{#if !error}<p class="text-xs text-[var(--slate-300)]">Loading…</p>{/if}
				{/each}
			</div>
		</ScrollArea>
	</Sheet.Content>
</Sheet.Root>

<Dialog.Root bind:open={previewOpen}>
	<Dialog.Content class="flex h-[80vh] flex-col sm:max-w-5xl">
		<Dialog.Header>
			<Dialog.Title>{preview?.name} · v{preview?.version}</Dialog.Title>
			<Dialog.Description>Read-only preview. Restoring loads it into the editor; save to make it the newest version.</Dialog.Description>
		</Dialog.Header>
		<div class="min-h-0 flex-1 border border-[var(--line)] bg-[var(--canvas)]">
			{#if preview}<MiniFlow flow={preview} />{/if}
		</div>
		<Dialog.Footer>
			<Button variant="ghost" onclick={() => (previewOpen = false)}>Close</Button>
			<Button class="btn-uv" onclick={restore}><RotateCcw /> Restore this version</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
