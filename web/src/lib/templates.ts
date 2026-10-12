import { goto } from '$app/navigation';
import { toast } from 'svelte-sonner';
import { api } from './api';
import { shell } from './shell.svelte';
import { errorMessage } from './state.svelte';

/** Copies a template into the user's flows and opens it in the editor. */
export async function useTemplate(id: string, token?: string) {
	try {
		const flow = await api.useTemplate(id, token);
		await shell.loadFlows();
		toast.success(`"${flow.name}" is yours to edit`);
		await goto(`/workflows/${encodeURIComponent(flow.id)}`);
	} catch (err) {
		toast.error(`Could not use the template: ${errorMessage(err)}`);
	}
}
