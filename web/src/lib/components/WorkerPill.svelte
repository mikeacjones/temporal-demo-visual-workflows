<script lang="ts">
	import { shell } from '$lib/shell.svelte';

	const pill = $derived(shell.pill);
</script>

<span
	class="status-badge flex items-center gap-1.5 !py-1"
	style:color={pill.color}
	data-tip={shell.worker
		? `${shell.worker.workers} worker(s) on task queue ${shell.session?.taskQueue ?? ''} (via ${shell.worker.source})${shell.worker.error ? ` · ${shell.worker.error}` : ''}`
		: ''}
>
	<span class="inline-block size-1.5" style:background="currentColor"></span>
	{pill.text}
</span>
