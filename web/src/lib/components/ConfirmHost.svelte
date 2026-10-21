<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { confirmState } from '$lib/confirm.svelte';

	// The one dialog behind confirmAction(). Closing it any other way
	// (Escape, the overlay, the close button) counts as cancel.
	const req = $derived(confirmState.request);
</script>

<Dialog.Root open={!!req} onOpenChange={(o) => !o && confirmState.answer(false)}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>{req?.title}</Dialog.Title>
			{#if req?.description}<Dialog.Description>{req.description}</Dialog.Description>{/if}
		</Dialog.Header>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => confirmState.answer(false)}>{req?.cancelLabel ?? 'Cancel'}</Button>
			<Button variant={req?.destructive ? 'destructive' : 'default'} onclick={() => confirmState.answer(true)}>{req?.confirmLabel ?? 'Confirm'}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
